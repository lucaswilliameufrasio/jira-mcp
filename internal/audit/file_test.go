package audit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jira-mcp/internal/mcp"
)

func TestFileStorePersistsIsolatesAndExpires(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	store, err := NewFileStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	ctx := context.Background()
	event := mcp.ToolEvent{Tool: "jira_search", StartedAt: now, Duration: time.Millisecond, Success: true}
	if err := store.Append(ctx, "user-a", event); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, "user-b", mcp.ToolEvent{Tool: "jira_create_issue", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reopened.clock = func() time.Time { return now }
	got, err := reopened.List(ctx, "user-a", 10)
	if err != nil || len(got) != 1 || got[0] != event {
		t.Fatalf("events = %#v, error = %v", got, err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "user-a") || strings.Contains(string(encoded), "argument") {
		t.Fatalf("unexpected payload: %s", encoded)
	}
	now = now.Add(time.Hour + time.Second)
	got, err = reopened.List(ctx, "user-a", 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("expired events = %#v, error = %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, identityHash("user-a")))
	if err != nil || len(entries) != 0 {
		t.Fatalf("expired files = %v, error = %v", entries, err)
	}
}

func TestFileStoreConcurrentWritersAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	first, err := NewFileStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFileStore(dir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := first
			if i%2 == 0 {
				store = second
			}
			if err := store.Append(context.Background(), "local", mcp.ToolEvent{Tool: "jira_search", StartedAt: time.Now(), Success: true}); err != nil {
				t.Errorf("append: %v", err)
			}
		}(i)
	}
	wg.Wait()
	events, err := first.List(context.Background(), "local", 100)
	if err != nil || len(events) != 40 {
		t.Fatalf("events count = %d, error = %v", len(events), err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode = %v, error = %v", info, err)
	}
}

func TestRetentionValidation(t *testing.T) {
	if got, err := ParseRetention(""); err != nil || got != DefaultRetention {
		t.Fatalf("default retention = %v, error = %v", got, err)
	}
	for _, value := range []string{"0s", "-1h", "1ms", "9000h", "invalid"} {
		if _, err := ParseRetention(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}
