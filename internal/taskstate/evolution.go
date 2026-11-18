package taskstate

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
)

type EvolutionContextItem struct {
	EvolutionID    string   `json:"evolution_id"`
	Type           string   `json:"type"`
	Statement      string   `json:"statement"`
	Scope          string   `json:"scope,omitempty"`
	Project        string   `json:"project,omitempty"`
	Device         string   `json:"device,omitempty"`
	Status         string   `json:"status"`
	Guided         bool     `json:"guided,omitempty"`
	ReviewRevision string   `json:"review_revision,omitempty"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
}

type EvolutionBinding struct {
	EvolutionID string    `json:"evolution_id"`
	OnSuccess   string    `json:"on_success"`
	OnFailure   string    `json:"on_failure"`
	BoundAt     time.Time `json:"bound_at"`
}

func (s *Store) SetGuidanceContext(id string, items []EvolutionContextItem) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		task.GuidanceContext = cloneEvolutionItems(items)
		seen := make(map[string]bool, len(task.EvolutionGuidanceSeen)+len(items))
		for _, evolutionID := range task.EvolutionGuidanceSeen {
			if evolutionID = strings.TrimSpace(evolutionID); evolutionID != "" {
				seen[evolutionID] = true
			}
		}
		for _, item := range items {
			if evolutionID := strings.TrimSpace(item.EvolutionID); evolutionID != "" {
				seen[evolutionID] = true
			}
		}
		task.EvolutionGuidanceSeen = task.EvolutionGuidanceSeen[:0]
		for evolutionID := range seen {
			task.EvolutionGuidanceSeen = append(task.EvolutionGuidanceSeen, evolutionID)
		}
		sort.Strings(task.EvolutionGuidanceSeen)
		task.UpdatedAt = now
		return nil
	})
}

func (s *Store) SetEvolutionCandidates(id, reviewRevision string, items []EvolutionContextItem) (Task, error) {
	return s.mutate(id, func(task *Task, now time.Time) error {
		if task.FinalReview == nil {
			return errors.New("final_review is required before evolution candidates")
		}
		if strings.TrimSpace(reviewRevision) == "" || task.FinalReview.ReviewRevision != strings.TrimSpace(reviewRevision) {
			return errors.New("review_revision does not match current final_review")
		}
		for i := range items {
			items[i].ReviewRevision = task.FinalReview.ReviewRevision
		}
		task.EvolutionCandidates = cloneEvolutionItems(items)
		task.UpdatedAt = now
		return nil
	})
}

func (s *Store) BindEvolution(id string, binding EvolutionBinding) (Task, error) {
	var err error
	binding, err = normalizeEvolutionBinding(binding)
	if err != nil {
		return Task{}, err
	}
	return s.mutate(id, func(task *Task, now time.Time) error {
		for _, existing := range task.EvolutionBindings {
			if existing.EvolutionID != binding.EvolutionID {
				continue
			}
			if existing.OnSuccess == binding.OnSuccess && existing.OnFailure == binding.OnFailure {
				return nil
			}
			return errors.New("evolution is already bound with different learning check semantics")
		}
		if taskExecutionStarted(*task) {
			return errors.New("learning check must be bound before task execution starts")
		}
		binding.BoundAt = now
		task.EvolutionBindings = append(task.EvolutionBindings, binding)
		task.UpdatedAt = now
		return nil
	})
}

func normalizeEvolutionBinding(binding EvolutionBinding) (EvolutionBinding, error) {
	binding.EvolutionID = strings.TrimSpace(binding.EvolutionID)
	binding.OnSuccess = strings.ToLower(strings.TrimSpace(binding.OnSuccess))
	binding.OnFailure = strings.ToLower(strings.TrimSpace(binding.OnFailure))
	binding.BoundAt = time.Time{}
	if binding.EvolutionID == "" {
		return EvolutionBinding{}, errors.New("evolution_id is required")
	}
	if !validLearningOutcome(binding.OnSuccess) || !validLearningOutcome(binding.OnFailure) {
		return EvolutionBinding{}, errors.New("learning check outcomes must be support, contradict or none")
	}
	if binding.OnSuccess == "none" && binding.OnFailure == "none" {
		return EvolutionBinding{}, errors.New("learning check must produce evidence for at least one outcome")
	}
	return binding, nil
}

func validLearningOutcome(value string) bool {
	switch value {
	case "support", "contradict", "none":
		return true
	default:
		return false
	}
}

func taskExecutionStarted(task Task) bool {
	if task.Status != StatusActive || task.Blocker != "" || task.FinalReview != nil || task.CompletedAt != nil {
		return true
	}
	for _, step := range task.Steps {
		if step.Status != StepPending {
			return true
		}
	}
	for _, event := range task.Events {
		switch event.Type {
		case "created", "templates_selected":
		default:
			return true
		}
	}
	return false
}

func cloneEvolutionItems(items []EvolutionContextItem) []EvolutionContextItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]EvolutionContextItem, len(items))
	for i, item := range items {
		item.EvidenceRefs = slices.Clone(item.EvidenceRefs)
		out[i] = item
	}
	return out
}
