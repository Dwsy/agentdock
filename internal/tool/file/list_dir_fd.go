package file

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	processcontrol "github.com/uvwt/agentdock/internal/process"
	"github.com/uvwt/agentdock/internal/workspace"
)

// fdListTimeout 与 search_text 的 rg 内核保持一致：目录枚举是纯读操作，
// 超时后直接按资源上限失败，不做静默降级。
const fdListTimeout = 30 * time.Second

type fdCandidate struct {
	abs   string
	rel   string
	isDir bool
}

// listDirFD 用 fd 做目录枚举，过滤判定、元数据与契约字段仍由 Go 侧同一套规则生成，
// 保证 fd 与 go_fallback 内核的输出契约一致。fd 不在 PATH 或 CLI 不被识别时返回
// available=false，由调用方回滚到 go_fallback 内核。
//
// 与 search_text 的 rg 内核一样，fd 自带的 ignore 机制（.ignore/.fdignore、全局
// gitignore）比 Go 回退内核更宽，会额外剪掉少量条目；Go 侧过滤始终是最终裁决。
func (svc *Service) listDirFD(ctx context.Context, root workspace.Path, opts listDirOptions) (Result, bool, error) {
	fdPath, err := exec.LookPath("fd")
	if err != nil {
		return nil, false, nil
	}
	// WalkDir 内核在根目录不可列举时直接失败；fd 对不可读根目录静默返回空，
	// 必须显式探测才能保持同样的失败语义。
	if err := probeDirReadable(root.Abs); err != nil {
		return nil, true, fmt.Errorf("read directory %s: %w", root.Abs, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, fdListTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, fdPath, fdListArgs(opts)...)
	cmd.Dir = root.Abs
	processcontrol.Configure(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, true, kernelExecutionError(runCtx, "fd", "directory search", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, true, kernelExecutionError(runCtx, "fd", "directory search", err)
	}

	candidates, truncated, stoppedEarly, skippedPaths, collectErr := svc.collectDirMatchesFD(stdout, root, opts)
	if stoppedEarly {
		// 命中 max_entries 上限后提前收工，杀掉 fd 避免继续枚举整棵子树。
		cancel()
	}
	waitErr := cmd.Wait()
	if collectErr == nil && waitErr != nil && !stoppedEarly {
		var exitErr *exec.ExitError
		// exit 2 是 clap 的参数解析失败：发行版自带的旧 fd 不认识新参数时按不可用回滚。
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 2 {
			return nil, false, nil
		}
		collectErr = waitErr
	}
	if collectErr != nil {
		return nil, true, kernelExecutionError(runCtx, "fd", "directory search", collectErr)
	}

	items := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate.abs)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				skippedPaths = append(skippedPaths, candidate.rel)
				continue
			}
			return nil, true, fmt.Errorf("inspect directory entry %s: %w", candidate.abs, err)
		}
		kind := "file"
		if info.IsDir() {
			kind = "directory"
		}
		items = append(items, map[string]any{
			"name":       filepath.Base(candidate.abs),
			"path":       candidate.rel,
			"type":       kind,
			"size_bytes": info.Size(),
			"modified":   info.ModTime().UTC().Format(time.RFC3339Nano),
			"is_hidden":  hiddenRelativePath(candidate.rel),
		})
	}
	sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["path"]) < fmt.Sprint(items[j]["path"]) })
	sort.Strings(skippedPaths)
	return Result{
		"path":          root.Display,
		"engine":        "fd",
		"entries":       items,
		"truncated":     truncated,
		"partial":       len(skippedPaths) > 0,
		"skipped_paths": skippedPaths,
	}, true, nil
}

func fdListArgs(opts listDirOptions) []string {
	// `--` 之后第一个参数是 pattern：正则 `.` 匹配所有条目，真正的过滤在 Go 侧完成，
	// 避免 fd 的 glob 语义差异影响契约（* 单段、** 跨目录）。
	args := []string{
		"--color=never", "--absolute-path", "--print0", "--no-ignore-parent",
		"--max-depth", strconv.Itoa(opts.MaxDepth),
	}
	if opts.IncludeHidden {
		args = append(args, "--hidden")
	}
	if opts.IncludeIgnored {
		args = append(args, "--no-ignore")
	} else {
		// 与 WalkDir 内核的 shouldSkipDir 同源：这些名字的条目既不进结果也不下钻。
		for _, name := range skippedDirNames {
			args = append(args, "--exclude", name)
		}
	}
	return args
}

// collectDirMatchesFD 流式消费 fd 的 NUL 分隔输出，应用与 go_fallback 相同的过滤规则。
// stoppedEarly 表示已拿到 max_entries+1 个匹配，调用方应终止 fd。
func (svc *Service) collectDirMatchesFD(r io.Reader, root workspace.Path, opts listDirOptions) (candidates []fdCandidate, truncated, stoppedEarly bool, skippedPaths []string, err error) {
	ignore := loadIgnoreMatcher(svc.ws.Root())
	candidates = make([]fdCandidate, 0, min(opts.MaxEntries, 200))
	// skipped_paths 的契约是数组：没有跳过路径时也必须是 []，不能是 null。
	skippedPaths = make([]string, 0)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	scanner.Split(splitNull)
	for scanner.Scan() {
		raw := scanner.Text()
		if raw == "" {
			continue
		}
		// fd 给目录输出加尾部斜杠；文件名本身不可能包含斜杠，直接去掉再定位。
		isDir := strings.HasSuffix(raw, "/")
		abs := strings.TrimSuffix(raw, "/")
		rel, relErr := relativePathFromRoot(root.Abs, abs)
		if relErr != nil {
			return nil, false, false, nil, fmt.Errorf("resolve directory entry %s: %w", abs, relErr)
		}
		if rel == "." {
			continue
		}
		if !opts.IncludeHidden && hiddenRelativePath(rel) {
			continue
		}
		if !opts.IncludeIgnored {
			ignoreRel, ignoreRelErr := svc.ws.Relative(abs)
			if ignoreRelErr != nil {
				return nil, false, false, nil, fmt.Errorf("resolve ignored directory entry %s: %w", abs, ignoreRelErr)
			}
			if shouldSkipDir(filepath.Base(abs)) || ignore.Ignored(ignoreRel, isDir) {
				continue
			}
		}
		// 权限探测限定在 WalkDir 内核真正会下钻的目录上（深度 < max_depth）：
		// fd 对不可读目录静默跳过，这里补回 skipped_paths 语义。
		if isDir && strings.Count(rel, "/")+1 < opts.MaxDepth {
			if probeErr := probeDirReadable(abs); probeErr != nil {
				if !errors.Is(probeErr, os.ErrPermission) {
					return nil, false, false, nil, fmt.Errorf("read directory %s: %w", abs, probeErr)
				}
				skippedPaths = append(skippedPaths, rel)
			}
		}
		kind := "file"
		if isDir {
			kind = "directory"
		}
		if opts.EntryType != "any" && opts.EntryType != kind {
			continue
		}
		if !matchesAny(rel, opts.Patterns) || matchesAny(rel, opts.ExcludePatterns) {
			continue
		}
		if len(candidates) >= opts.MaxEntries {
			truncated = true
			stoppedEarly = true
			break
		}
		candidates = append(candidates, fdCandidate{abs: abs, rel: rel, isDir: isDir})
	}
	if err := scanner.Err(); err != nil {
		return nil, false, false, nil, err
	}
	return candidates, truncated, stoppedEarly, skippedPaths, nil
}

// probeDirReadable 验证目录能否被列举。WalkDir 内核靠 ReadDir 失败发现权限问题，
// fd 则静默跳过，所以 fd 内核需要显式探测才能输出同样的 skipped_paths。
func probeDirReadable(abs string) error {
	dir, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer dir.Close()
	if _, err := dir.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func splitNull(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
