//go:build integration

package remote

import (
	"context"
	"os"
	"testing"
	"time"
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
