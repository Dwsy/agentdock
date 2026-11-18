package evolution

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/taskstate"
)

type fakeLifecycleStore struct {
	mu      sync.Mutex
	records map[string]Record
}

func newFakeLifecycleServer(t *testing.T) (*httptest.Server, *fakeLifecycleStore) {
	t.Helper()
	store := &fakeLifecycleStore{records: map[string]Record{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		store.mu.Lock()
		defer store.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/recall/lifecycle/query":
			var query Query
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Fatal(err)
			}
			items := make([]Record, 0)
			for _, record := range store.records {
				if query.EvolutionID != "" && record.EvolutionID != query.EvolutionID {
					continue
				}
				if query.Query != "" && !strings.Contains(strings.ToLower(record.Statement+" "+record.CanonicalKey), strings.ToLower(query.Query)) {
					continue
				}
				items = append(items, record)
			}
			_ = json.NewEncoder(w).Encode(queryResult{Records: items, Count: len(items)})
		case "/internal/recall/lifecycle/transition":
			var req transitionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			current := store.records[req.Record.EvolutionID]
			if current.Revision != req.ExpectedRevision {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "LIFECYCLE_REVISION_CONFLICT", "error": "stale"})
				return
			}
			record := req.Record
			record.Revision = current.Revision + 1
			store.records[record.EvolutionID] = record
			_ = json.NewEncoder(w).Encode(transitionResult{Record: record})
		default:
			http.NotFound(w, r)
		}
	}))
	return server, store
}

func newTestService(t *testing.T) (*Service, *taskstate.Store, *httptest.Server) {
	t.Helper()
	server, _ := newFakeLifecycleServer(t)
	tasks, err := taskstate.New(t.TempDir())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	service := New(func() config.Config {
		return config.Config{NexusEndpoint: server.URL, NexusDeviceToken: "test-device-token"}
	}, tasks)
	return service, tasks, server
}

func reviewedTask(t *testing.T, tasks *taskstate.Store, fact string) taskstate.Task {
	t.Helper()
	task := newLearningTask(t, tasks)
	return passLearningTask(t, tasks, task, fact)
}

func newLearningTask(t *testing.T, tasks *taskstate.Store) taskstate.Task {
	t.Helper()
	task, err := tasks.CreateWithContext("验证经验", "验证一条经验", "agentdock", "", []string{"有真实结果"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func bindLearningCheck(t *testing.T, service *Service, task taskstate.Task, evolutionID, onSuccess, onFailure string) {
	t.Helper()
	_, err := service.Manage(t.Context(), Request{
		Intent: "bind", EvolutionID: evolutionID, TaskID: task.ID,
		LearningCheck: &LearningCheck{OnSuccess: onSuccess, OnFailure: onFailure},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func passLearningTask(t *testing.T, tasks *taskstate.Store, task taskstate.Task, fact string) taskstate.Task {
	t.Helper()
	updated, err := tasks.FinalReview(task.ID, taskstate.FinalReviewInput{Status: taskstate.FinalReviewPass, Summary: "验证完成", VerifiedFacts: []string{fact}})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func failLearningTask(t *testing.T, tasks *taskstate.Store, task taskstate.Task, risk string) taskstate.Task {
	t.Helper()
	updated, err := tasks.FinalReview(task.ID, taskstate.FinalReviewInput{Status: taskstate.FinalReviewFailed, Summary: "验证失败", OpenRisks: []string{risk}})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestStageOnePolicyAndStageThreeCannotBypassProvisional(t *testing.T) {
	service, _, server := newTestService(t)
	defer server.Close()

	preference, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "preference", Statement: "README 面向用户", Project: "agentdock"}})
	if err != nil {
		t.Fatal(err)
	}
	if preference.Record == nil || preference.Record.Status != StatusActive {
		t.Fatalf("preference = %#v", preference)
	}

	stage3, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "preference", Statement: "模型补漏候选", Project: "agentdock", Source: "nexus-stage3"}})
	if err != nil {
		t.Fatal(err)
	}
	if stage3.Record == nil || stage3.Record.Status != StatusProvisional {
		t.Fatalf("stage3 = %#v", stage3)
	}
}

func TestConcurrentExactProposalsConvergeOnOneStableEvolutionID(t *testing.T) {
	server, store := newFakeLifecycleServer(t)
	defer server.Close()
	tasks, err := taskstate.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(func() config.Config {
		return config.Config{NexusEndpoint: server.URL, NexusDeviceToken: "test-device-token"}
	}, tasks)
	candidate := Candidate{Type: "runbook", Statement: "并发节点必须收敛到同一经验", Project: "agentdock", Device: "mini"}

	const workers = 16
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &candidate})
			if err != nil {
				errs <- err
				return
			}
			ids <- result.Record.EvolutionID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent propose error: %v", err)
	}

	normalized, err := normalizeCandidate(candidate)
	if err != nil {
		t.Fatal(err)
	}
	wantID := candidateEvolutionID(normalized)
	for id := range ids {
		if id != wantID {
			t.Fatalf("evolution_id=%q want=%q", id, wantID)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.records) != 1 {
		t.Fatalf("records=%d want=1", len(store.records))
	}
}

func TestStageTwoIndependentEvidencePromotesAndContradictionQuarantines(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()
	proposed, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "bug_pattern", Statement: "等待 readiness 再判成功", Project: "agentdock"}})
	if err != nil {
		t.Fatal(err)
	}
	id := proposed.Record.EvolutionID

	wantStatuses := []string{StatusProvisional, StatusActive, StatusVerified}
	for i, want := range wantStatuses {
		task := newLearningTask(t, tasks)
		bindLearningCheck(t, service, task, id, RelationSupport, RelationContradict)
		task = passLearningTask(t, tasks, task, "第 "+string(rune('1'+i))+" 次独立验证通过")
		if err := service.ResolveBindings(t.Context(), task); err != nil {
			t.Fatal(err)
		}
		record, err := service.get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != want {
			t.Fatalf("support #%d status=%s want=%s", i+1, record.Status, want)
		}
	}

	contradiction := newLearningTask(t, tasks)
	bindLearningCheck(t, service, contradiction, id, RelationNone, RelationContradict)
	contradiction = failLearningTask(t, tasks, contradiction, "出现明确反例")
	if err := service.ResolveBindings(t.Context(), contradiction); err != nil {
		t.Fatal(err)
	}
	record, err := service.get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != StatusQuarantine {
		t.Fatalf("contradiction status=%s", record.Status)
	}
}

func TestGuidedExperienceCannotSupportItselfAndReviewCannotVoteTwice(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()
	proposed, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "runbook", Statement: "按固定流程发布", Project: "agentdock"}})
	if err != nil {
		t.Fatal(err)
	}
	id := proposed.Record.EvolutionID

	task, err := tasks.CreateWithContext("发布", "按经验发布", "agentdock", "", []string{"完成"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SetGuidanceContext(task.ID, []taskstate.EvolutionContextItem{{EvolutionID: id, Guided: true, Status: StatusActive}}); err != nil {
		t.Fatal(err)
	}
	_, err = service.Manage(t.Context(), Request{Intent: "bind", EvolutionID: id, TaskID: task.ID, LearningCheck: &LearningCheck{OnSuccess: RelationSupport, OnFailure: RelationContradict}})
	if !errors.Is(err, ErrSelfProof) {
		t.Fatalf("self proof bind err=%v", err)
	}

	other := newLearningTask(t, tasks)
	bindLearningCheck(t, service, other, id, RelationSupport, RelationContradict)
	other = passLearningTask(t, tasks, other, "独立成功")
	if err := service.ResolveBindings(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	first, err := service.get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResolveBindings(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	second, err := service.get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if second.SupportCount != first.SupportCount {
		t.Fatalf("same task voted twice: first=%d second=%d", first.SupportCount, second.SupportCount)
	}
}

func TestSameTaskCannotVoteAgainAfterFinalReviewRevisionChanges(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()
	proposed, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "runbook", Statement: "同一执行只能算一次独立证据", Project: "agentdock"}})
	if err != nil {
		t.Fatal(err)
	}
	id := proposed.Record.EvolutionID

	task, err := tasks.CreateWithContext("一次执行", "验证一次独立执行", "agentdock", "", []string{"完成验证"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	bindLearningCheck(t, service, task, id, RelationSupport, RelationContradict)
	task, err = tasks.FinalReview(task.ID, taskstate.FinalReviewInput{Status: taskstate.FinalReviewFailed, Summary: "第一次评审发现反例", OpenRisks: []string{"真实结果与经验矛盾"}})
	if err != nil {
		t.Fatal(err)
	}
	firstRevision := task.FinalReview.ReviewRevision
	if err := service.ResolveBindings(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	task, err = tasks.FinalReview(task.ID, taskstate.FinalReviewInput{Status: taskstate.FinalReviewPass, Summary: "同一执行重新评审为通过", VerifiedFacts: []string{"同一执行现在被解释为支持"}})
	if err != nil {
		t.Fatal(err)
	}
	if task.FinalReview.ReviewRevision == firstRevision {
		t.Fatal("expected a new final_review revision")
	}
	err = service.ResolveBindings(t.Context(), task)
	if err == nil || !strings.Contains(err.Error(), "already assessed") {
		t.Fatalf("same task second revision vote err=%v", err)
	}
}

func TestStageTwoRejectsUnboundPostHocAssessment(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()
	proposed, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "runbook", Statement: "未绑定执行不能事后投票", Project: "agentdock"}})
	if err != nil {
		t.Fatal(err)
	}
	task := reviewedTask(t, tasks, "执行完成后才试图决定关系")
	_, err = service.assess(t.Context(), Request{
		EvolutionID: proposed.Record.EvolutionID, TaskID: task.ID, ReviewRevision: task.FinalReview.ReviewRevision,
		Relation: RelationSupport, EvidenceRefs: reviewEvidenceRefs(task, true),
	})
	if err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("unbound assessment error = %v", err)
	}
}

func TestStageTwoRejectsBindAfterExecutionStarted(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()
	proposed, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "runbook", Statement: "必须执行前绑定", Project: "agentdock"}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := tasks.CreateWithContext("执行", "先执行再尝试绑定", "agentdock", "", []string{"完成"}, []taskstate.TaskStepInput{{ID: "run", Title: "Run"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Checkpoint(task.ID, "run", taskstate.StepInProgress, "execution started"); err != nil {
		t.Fatal(err)
	}
	_, err = service.Manage(t.Context(), Request{Intent: "bind", EvolutionID: proposed.Record.EvolutionID, TaskID: task.ID, LearningCheck: &LearningCheck{OnSuccess: RelationSupport, OnFailure: RelationContradict}})
	if err == nil || !strings.Contains(err.Error(), "before task execution starts") {
		t.Fatalf("late bind error = %v", err)
	}
}

func TestStageTwoRejectsRelationDifferentFromPreboundOutcome(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()
	proposed, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "runbook", Statement: "结果关系必须来自预绑定", Project: "agentdock"}})
	if err != nil {
		t.Fatal(err)
	}
	task := newLearningTask(t, tasks)
	bindLearningCheck(t, service, task, proposed.Record.EvolutionID, RelationSupport, RelationContradict)
	task = passLearningTask(t, tasks, task, "执行成功")
	_, err = service.assess(t.Context(), Request{
		EvolutionID: proposed.Record.EvolutionID, TaskID: task.ID, ReviewRevision: task.FinalReview.ReviewRevision,
		Relation: RelationContradict, EvidenceRefs: reviewEvidenceRefs(task, true),
	})
	if err == nil || !strings.Contains(err.Error(), "does not match pre-bound") {
		t.Fatalf("mismatched relation error = %v", err)
	}
}

func TestStageTwoNoneOutcomeProducesNoEvidence(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()
	proposed, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "runbook", Statement: "某些执行结果不应投票", Project: "agentdock"}})
	if err != nil {
		t.Fatal(err)
	}
	task := newLearningTask(t, tasks)
	bindLearningCheck(t, service, task, proposed.Record.EvolutionID, RelationNone, RelationContradict)
	task = passLearningTask(t, tasks, task, "成功结果声明为 none")
	if err := service.ResolveBindings(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	record, err := service.get(t.Context(), proposed.Record.EvolutionID)
	if err != nil {
		t.Fatal(err)
	}
	if record.SupportCount != 0 || record.ContradictCount != 0 || record.Status != StatusProvisional {
		t.Fatalf("none outcome changed lifecycle: %#v", record)
	}
}

func TestDerivedAssetCandidatesAlwaysStartProvisional(t *testing.T) {
	service, _, server := newTestService(t)
	defer server.Close()

	for _, kind := range []string{"workflow_template", "skill"} {
		result, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{
			Type: kind, Statement: "固化为可复用资产 " + kind, Project: "agentdock", Source: "user-explicit",
		}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Record == nil || result.Record.Status != StatusProvisional {
			t.Fatalf("%s result = %#v", kind, result)
		}
	}
}

func TestUnknownCandidateTypeRejected(t *testing.T) {
	service, _, server := newTestService(t)
	defer server.Close()

	_, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{
		Type: "arbitrary_model_state", Statement: "模型自定义资产类型", Project: "agentdock",
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported candidate type") {
		t.Fatalf("error = %v", err)
	}
}

func TestWorkflowDerivedFromCandidateCannotSupportSourceEvolution(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()
	proposed, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{
		Type: "workflow_template", Statement: "把发布流程固化为工作流", Project: "agentdock",
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := proposed.Record.EvolutionID

	task, err := tasks.CreateWithContext("按派生模板发布", "使用派生模板", "agentdock", "", []string{"完成"}, nil, []taskstate.TemplateReference{{ID: "release", Version: "1.0.0", SourceEvolutionID: id}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Manage(t.Context(), Request{Intent: "bind", EvolutionID: id, TaskID: task.ID, LearningCheck: &LearningCheck{OnSuccess: RelationSupport, OnFailure: RelationContradict}})
	if !errors.Is(err, ErrSelfProof) {
		t.Fatalf("derived workflow self proof bind err=%v", err)
	}
}

func TestEvolutionScopeIsHardFilteredForGuidanceAndAssessment(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()

	global, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "preference", Statement: "release safe release safe done", Project: "global", Scope: "shared"}})
	if err != nil {
		t.Fatal(err)
	}
	agentdock, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "preference", Statement: "release safe release safe done", Project: "agentdock"}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "preference", Statement: "release safe release safe done", Project: "other-project"}})
	if err != nil {
		t.Fatal(err)
	}
	deviceOnly, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{Type: "preference", Statement: "release safe release safe done", Project: "agentdock", Device: "mini", Scope: "device"}})
	if err != nil {
		t.Fatal(err)
	}

	task, err := tasks.CreateWithContext("release safe", "release safe", "agentdock", "air", []string{"done"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	guidance, err := service.Guidance(t.Context(), task)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range guidance {
		seen[item.EvolutionID] = true
	}
	if !seen[global.Record.EvolutionID] || !seen[agentdock.Record.EvolutionID] {
		t.Fatalf("expected global and same-project guidance, got %#v", guidance)
	}
	if seen[other.Record.EvolutionID] || seen[deviceOnly.Record.EvolutionID] {
		t.Fatalf("cross-project/device guidance leaked: %#v", guidance)
	}

	_, err = service.Manage(t.Context(), Request{Intent: "bind", EvolutionID: other.Record.EvolutionID, TaskID: task.ID, LearningCheck: &LearningCheck{OnSuccess: RelationSupport, OnFailure: RelationContradict}})
	if err == nil || !strings.Contains(err.Error(), "scope does not apply") {
		t.Fatalf("cross-project bind error = %v", err)
	}
}

func TestClientDecodesNexusLifecycleOperationMetadata(t *testing.T) {
	const (
		evolutionID = "evo_0123456789abcdef"
		operationID = "op_0123456789abcdef"
	)
	digest := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/internal/recall/lifecycle/query" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"records": []any{map[string]any{
				"evolution_id":   evolutionID,
				"title":          "contract",
				"statement":      "Nexus lifecycle operation metadata keeps its digest",
				"type":           "runbook",
				"scope":          "project",
				"project":        "agentdock",
				"status":         StatusProvisional,
				"policy_version": PolicyVersion,
				"revision":       1,
				"applied_operations": []any{map[string]any{
					"id": operationID, "digest": digest,
				}},
			}},
			"count": 1,
		})
	}))
	defer server.Close()

	client := client{config: func() config.Config {
		return config.Config{NexusEndpoint: server.URL, NexusDeviceToken: "test-device-token"}
	}}
	records, err := client.query(t.Context(), Query{EvolutionID: evolutionID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || len(records[0].AppliedOperations) != 1 {
		t.Fatalf("records = %#v", records)
	}
	if got := records[0].AppliedOperations[0]; got.ID != operationID || got.Digest != digest {
		t.Fatalf("applied operation = %#v", got)
	}
}

func TestBlindedCreateTimeValidationCanPromoteActiveToVerified(t *testing.T) {
	service, tasks, server := newTestService(t)
	defer server.Close()
	// 让 fake lifecycle query 能在普通 Task 上精确命中这条经验。
	proposed, err := service.Manage(t.Context(), Request{Intent: "propose", Candidate: &Candidate{
		Type: "runbook", Statement: "验证经验 验证一条经验 有真实结果", Project: "agentdock",
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := proposed.Record.EvolutionID

	for i := 0; i < 2; i++ {
		task := newLearningTask(t, tasks)
		bindLearningCheck(t, service, task, id, RelationSupport, RelationContradict)
		task = passLearningTask(t, tasks, task, "独立支持")
		if err := service.ResolveBindings(t.Context(), task); err != nil {
			t.Fatal(err)
		}
	}
	record, err := service.get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != StatusActive || record.SupportCount != 2 {
		t.Fatalf("precondition record = %#v", record)
	}

	// 普通第三个 Task 会收到 active Guidance，因此仍然不能 support 自证。
	ordinary := newLearningTask(t, tasks)
	guidance, err := service.Guidance(t.Context(), ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if len(guidance) != 1 || guidance[0].EvolutionID != id {
		t.Fatalf("ordinary task guidance = %#v", guidance)
	}
	ordinary, err = tasks.SetGuidanceContext(ordinary.ID, guidance)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Manage(t.Context(), Request{
		Intent: "bind", EvolutionID: id, TaskID: ordinary.ID,
		LearningCheck: &LearningCheck{OnSuccess: RelationSupport, OnFailure: RelationContradict},
	})
	if !errors.Is(err, ErrSelfProof) {
		t.Fatalf("ordinary guided task support bind err=%v", err)
	}

	// 显式验证 Task 在创建前完成 learning check 预检并原子落盘。
	bindings, err := service.ValidateBindings(t.Context(), taskstate.Task{Project: "agentdock"}, []taskstate.EvolutionBinding{{
		EvolutionID: id, OnSuccess: RelationSupport, OnFailure: RelationContradict,
	}})
	if err != nil {
		t.Fatal(err)
	}
	blind, err := tasks.CreateWithContext("验证经验", "验证一条经验", "agentdock", "", []string{"有真实结果"}, nil, nil, bindings...)
	if err != nil {
		t.Fatal(err)
	}
	guidance, err = service.Guidance(t.Context(), blind)
	if err != nil {
		t.Fatal(err)
	}
	if len(guidance) != 0 {
		t.Fatalf("blinded validation leaked target guidance: %#v", guidance)
	}
	blind, err = tasks.SetGuidanceContext(blind.ID, guidance)
	if err != nil {
		t.Fatal(err)
	}
	// Resume/refresh 语义仍由 durable binding 驱动，目标不会在后续刷新时重新注入。
	guidance, err = service.Guidance(t.Context(), blind)
	if err != nil {
		t.Fatal(err)
	}
	if len(guidance) != 0 {
		t.Fatalf("blinded target leaked on later guidance refresh: %#v", guidance)
	}
	blind = passLearningTask(t, tasks, blind, "第三次独立盲测通过")
	if err := service.ResolveBindings(t.Context(), blind); err != nil {
		t.Fatal(err)
	}
	record, err = service.get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != StatusVerified || record.SupportCount != 3 {
		t.Fatalf("blinded third support record = %#v", record)
	}
	if taskGuided(blind, id) {
		t.Fatalf("blinded validation task unexpectedly marked target as guided: %#v", blind.EvolutionGuidanceSeen)
	}
}

func TestTransitionRecognizesWorkersStructuredConflictError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"LIFECYCLE_REVISION_CONFLICT","message":"lifecycle revision conflict"}}`))
	}))
	defer server.Close()

	c := client{config: func() config.Config {
		return config.Config{NexusEndpoint: server.URL, NexusDeviceToken: "test-device-token"}
	}}
	_, err := c.transition(t.Context(), transitionRequest{
		OperationID: "op_conflict",
		Record:      Record{EvolutionID: "evo_1234567890abcdef"},
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("err = %v, want ErrRevisionConflict", err)
	}
}
