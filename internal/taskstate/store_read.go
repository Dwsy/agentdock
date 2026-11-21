package taskstate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

func (s *Store) Get(id string) (Task, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return Task{}, err
	}
	defer release()
	return s.loadLocked(id)
}

func (s *Store) Delete(id string) (Task, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return Task{}, err
	}
	defer release()

	task, err := s.loadLocked(id)
	if err != nil {
		return Task{}, err
	}
	if err := os.Remove(filepath.Join(s.root, id+".json")); err != nil {
		return Task{}, fmt.Errorf("delete task %s: %w", id, err)
	}
	delete(s.listCache, id+".json")
	return task, nil
}

func (s *Store) List(status Status, limit int) ([]Task, error) {
	return s.ListContext(context.Background(), status, limit)
}

func (s *Store) ListContext(ctx context.Context, status Status, limit int) ([]Task, error) {
	release, err := s.acquireStoreLockContext(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if limit <= 0 {
		limit = 50
	} else if limit > MaxListLimit {
		limit = MaxListLimit
	}
	var matched []*Task
	if err := s.scanTasksLocked(ctx, func(task *Task) error {
		if status == "" || task.Status == status {
			matched = append(matched, task)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].UpdatedAt.After(matched[j].UpdatedAt) })
	if len(matched) > limit {
		matched = matched[:limit]
	}
	tasks := make([]Task, len(matched))
	for i, task := range matched {
		tasks[i] = cloneTask(*task)
	}
	return tasks, nil
}

// OverviewRecentWindow 是概览页「近期活跃」统计的时间窗。运行时响应会显式
// 下发窗口小时数，控制台不复制这个常量。
const OverviewRecentWindow = 24 * time.Hour

// OverviewCounts 是任务概览统计及其取样范围。Matched 是扫描到的任务总数，
// Limit 是本次统计按更新时间倒序允许纳入的最大条数。
type OverviewCounts struct {
	Active       int
	Completed    int
	Blocked      int
	ActiveRecent int
	Matched      int
	Limit        int
}

type taskOverviewMeta struct {
	status    Status
	updatedAt time.Time
}

// OverviewCountsContext computes task status counts without deep-cloning full task payloads.
func (s *Store) OverviewCountsContext(ctx context.Context, limit int) (OverviewCounts, error) {
	release, err := s.acquireStoreLockContext(ctx)
	if err != nil {
		return OverviewCounts{}, err
	}
	defer release()
	if limit <= 0 {
		limit = 200
	} else if limit > MaxListLimit {
		limit = MaxListLimit
	}
	var items []taskOverviewMeta
	if err := s.scanTasksLocked(ctx, func(task *Task) error {
		items = append(items, taskOverviewMeta{status: task.Status, updatedAt: task.UpdatedAt})
		return nil
	}); err != nil {
		return OverviewCounts{}, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].updatedAt.After(items[j].updatedAt) })
	counts := OverviewCounts{Matched: len(items), Limit: limit}
	if len(items) > limit {
		items = items[:limit]
	}
	recentCutoff := time.Now().UTC().Add(-OverviewRecentWindow)
	for _, item := range items {
		switch item.status {
		case StatusActive:
			counts.Active++
		case StatusCompleted:
			counts.Completed++
		case StatusBlocked:
			counts.Blocked++
		}
		if item.status == StatusActive && !item.updatedAt.Before(recentCutoff) {
			counts.ActiveRecent++
		}
	}
	return counts, nil
}

func (s *Store) scanTasksLocked(ctx context.Context, visit func(task *Task) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "tsk_") || filepath.Ext(name) != ".json" {
			continue
		}
		seen[name] = struct{}{}
		info, err := entry.Info()
		if err != nil {
			slog.Warn("skip unreadable task state", "file", name, "error", err)
			continue
		}
		if info.Size() > maxTaskStateFileBytes {
			slog.Warn("skip oversized task state", "file", name, "size", info.Size())
			continue
		}
		if s.listCache != nil {
			// os.SameFile + size + ModTime safely invalidates across atomicfile.Write renames
			// even if an external process updated the file outside this Store instance.
			if cached, ok := s.listCache[name]; ok &&
				os.SameFile(cached.info, info) &&
				cached.info.Size() == info.Size() &&
				cached.info.ModTime().Equal(info.ModTime()) {
				if err := visit(&cached.task); err != nil {
					return err
				}
				continue
			}
		}
		data, err := readTaskStateFile(filepath.Join(s.root, name))
		if err != nil {
			slog.Warn("skip unreadable task state", "file", name, "error", err)
			continue
		}
		task, err := decodeTask(data, name)
		if err != nil {
			slog.Warn("skip invalid task state", "file", name, "error", err)
			continue
		}
		if s.listCache == nil {
			s.listCache = make(map[string]*cachedTaskFile, len(entries))
		}
		entryCache := &cachedTaskFile{info: info, task: cloneTask(task)}
		s.listCache[name] = entryCache
		if err := visit(&entryCache.task); err != nil {
			return err
		}
	}
	if len(s.listCache) > len(seen) {
		for name := range s.listCache {
			if _, ok := seen[name]; !ok {
				delete(s.listCache, name)
			}
		}
	}
	return nil
}

func cloneTask(task Task) Task {
	task.Conditions = slices.Clone(task.Conditions)
	if task.Template != nil {
		tpl := *task.Template
		tpl.Candidates = slices.Clone(tpl.Candidates)
		task.Template = &tpl
	}
	task.SourceTemplates = slices.Clone(task.SourceTemplates)
	task.Steps = slices.Clone(task.Steps)
	task.Events = slices.Clone(task.Events)
	task.GuidanceContext = cloneEvolutionItems(task.GuidanceContext)
	task.EvolutionGuidanceSeen = slices.Clone(task.EvolutionGuidanceSeen)
	task.EvolutionCandidates = cloneEvolutionItems(task.EvolutionCandidates)
	task.EvolutionBindings = slices.Clone(task.EvolutionBindings)
	if task.FinalReview != nil {
		review := *task.FinalReview
		review.VerifiedFacts = slices.Clone(review.VerifiedFacts)
		review.OpenRisks = slices.Clone(review.OpenRisks)
		review.MissingChecks = slices.Clone(review.MissingChecks)
		task.FinalReview = &review
	}
	if task.CompletedAt != nil {
		completedAt := *task.CompletedAt
		task.CompletedAt = &completedAt
	}
	return task
}

func (s *Store) loadLocked(id string) (Task, error) {
	if err := validateID(id); err != nil {
		return Task{}, err
	}
	data, err := readTaskStateFile(filepath.Join(s.root, id+".json"))
	if os.IsNotExist(err) {
		return Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	if err != nil {
		return Task{}, err
	}
	task, err := decodeTask(data, id)
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *Store) saveLocked(task Task) error {
	if err := validateID(task.ID); err != nil {
		return err
	}
	if len(task.Events) > maxTaskEvents {
		task.Events = append([]Event(nil), task.Events[len(task.Events)-maxTaskEvents:]...)
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxTaskStateFileBytes {
		return fmt.Errorf("task state exceeds %d bytes", maxTaskStateFileBytes)
	}
	target := filepath.Join(s.root, task.ID+".json")
	delete(s.listCache, task.ID+".json")
	return atomicfile.Write(target, data, 0o600)
}

func readTaskStateFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxTaskStateFileBytes {
		return nil, fmt.Errorf("task state exceeds %d bytes", maxTaskStateFileBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxTaskStateFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTaskStateFileBytes {
		return nil, fmt.Errorf("task state exceeds %d bytes", maxTaskStateFileBytes)
	}
	return data, nil
}

func decodeTask(data []byte, label string) (Task, error) {
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return Task{}, fmt.Errorf("decode task %s: %w", label, err)
	}
	if task.SchemaVersion != SchemaVersion {
		return Task{}, fmt.Errorf("unsupported task schema version %d", task.SchemaVersion)
	}
	if len(task.Events) > maxTaskEvents {
		task.Events = append([]Event(nil), task.Events[len(task.Events)-maxTaskEvents:]...)
	}
	return task, nil
}
