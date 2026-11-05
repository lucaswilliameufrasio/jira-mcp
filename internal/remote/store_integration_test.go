//go:build integration

package remote

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"jira-mcp/internal/mcp"
)

func TestValkeyStoreRoundTrip(t *testing.T) {
	rawURL := os.Getenv("VALKEY_URL")
	if rawURL == "" {
		t.Skip("VALKEY_URL is required for integration tests")
	}
	store, err := NewValkeyStore(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	key := "jira-mcp:test:store:" + time.Now().Format("20060102150405.000000000")
	want := map[string]string{"value": "ok"}
	if err := store.Put(ctx, key, want, time.Minute); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := store.Get(ctx, key, &got); err != nil {
		t.Fatal(err)
	}
	if got["value"] != "ok" {
		t.Fatalf("stored value = %#v", got)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := store.Get(ctx, key, &got); err != ErrNotFound {
		t.Fatalf("get after delete = %v", err)
	}
}

func TestValkeyAuditConcurrentIsolationAndExpiry(t *testing.T) {
	rawURL := os.Getenv("VALKEY_URL")
	if rawURL == "" {
		t.Skip("VALKEY_URL is required for integration tests")
	}
	store, err := NewValkeyStore(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	audits, err := NewValkeyAuditStore(store, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	identity := fmt.Sprintf("test-%d", time.Now().UnixNano())
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := audits.Append(ctx, identity, mcp.ToolEvent{Tool: "jira_search", StartedAt: time.Now(), Success: true}); err != nil {
				t.Errorf("append: %v", err)
			}
		}()
	}
	wg.Wait()
	events, err := audits.List(ctx, identity, 100)
	if err != nil || len(events) != 40 {
		t.Fatalf("events count = %d, err = %v", len(events), err)
	}
	other, err := audits.List(ctx, identity+"-other", 100)
	if err != nil || len(other) != 0 {
		t.Fatalf("other events = %#v, err = %v", other, err)
	}
	time.Sleep(2200 * time.Millisecond)
	events, err = audits.List(ctx, identity, 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("expired events = %#v, err = %v", events, err)
	}
}
