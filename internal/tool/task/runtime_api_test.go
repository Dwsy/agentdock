package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeOverviewCountsTaskStatesAndRecentActiveTasks(t *testing.T) {
	service, root := newTaskTestService(t)

	_, err := service.tasks.Create("recent", "recent active", []string{"done"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := service.tasks.Create("old", "old active", []string{"done"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := service.tasks.Create("blocked", "blocked task", []string{"done"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.tasks.Block(blocked.ID, "waiting"); err != nil {
		t.Fatal(err)
	}

	// 用真实 Task JSON 只调整隔离测试目录里的更新时间，验证 24h 边界不会把旧任务算进 recent。
	old.UpdatedAt = time.Now().UTC().Add(-25 * time.Hour)
	data, err := json.MarshalIndent(old, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, ".agentdock", "tasks", old.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	counts, err := service.RuntimeOverview()
	if err != nil {
		t.Fatal(err)
	}
	if counts["active"] != 2 || counts["blocked"] != 1 || counts["completed"] != 0 || counts["active_recent_24h"] != 1 {
		t.Fatalf("overview counts = %#v", counts)
	}
	scope, ok := counts["scope"].(map[string]any)
	if !ok {
		t.Fatalf("overview scope = %#v", counts["scope"])
	}
	if scope["limit"] != runtimeOverviewTaskLimit || scope["matched"] != 3 || scope["truncated"] != false {
		t.Fatalf("overview scope = %#v, want limit=%d matched=3 truncated=false", scope, runtimeOverviewTaskLimit)
	}
	if scope["recent_window_hours"] != 24 {
		t.Fatalf("overview recent window = %#v, want 24h", scope["recent_window_hours"])
	}
}
