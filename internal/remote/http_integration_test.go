//go:build integration

package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRemoteMCPListsToolsForAuthenticatedUser(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	key := make([]byte, 32)
	accessToken, err := seal(key, "atlassian-access-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "mcp:user:user-1", userRecord{AccountID: "account-1", CloudID: "cloud-1", Tools: []string{"jira_search"}, AccessToken: accessToken, ExpiresAt: time.Now().Add(time.Hour)}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "mcp:token:mcp-token", accessSession{UserID: "user-1"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{PublicURL: "https://mcp.example", AtlassianID: "client", AtlassianKey: "secret", Store: store, EncryptionKey: key})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "integration-client", Version: "1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: "mcp-token", base: http.DefaultTransport}}, DisableStandaloneSSE: true}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "jira_search" {
		t.Fatalf("tools = %#v", listed.Tools)
	}
}

func TestRemoteMCPAuditPersistsForAuthenticatedIdentity(t *testing.T) {
	rawURL := os.Getenv("VALKEY_URL")
	if rawURL == "" {
		t.Skip("VALKEY_URL is required")
	}
	store, err := NewValkeyStore(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	audits, err := NewValkeyAuditStore(store, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/search/jql") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"issues":[],"isLast":true}`))
	}))
	defer fake.Close()
	key := make([]byte, 32)
	token, err := seal(key, "synthetic-access-token")
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("audit-test-%d", time.Now().UnixNano())
	account := id + "-account"
	cloud := id + "-cloud"
	ctx := context.Background()
	if err := store.Put(ctx, "mcp:user:"+id, userRecord{AccountID: account, CloudID: cloud, Tools: []string{"jira_search"}, AccessToken: token, ExpiresAt: time.Now().Add(time.Hour)}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, "mcp:token:"+id, accessSession{UserID: id}, time.Minute); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Delete(ctx, "mcp:user:"+id)
		_ = store.Delete(ctx, "mcp:token:"+id)
	})
	server, err := New(Config{PublicURL: "https://mcp.example", AtlassianID: "client", AtlassianKey: "secret", AtlassianAPIURL: fake.URL, Store: store, Audit: audits, EncryptionKey: key})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-client", Version: "1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: id, base: http.DefaultTransport}}, DisableStandaloneSSE: true}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "jira_search", Arguments: map[string]any{"jql": "project = PRIVATE"}})
	if err != nil || result.IsError {
		t.Fatalf("call failed: %v", err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "jira_search", Arguments: map[string]any{"jql": ""}})
	if err != nil || !result.IsError {
		t.Fatalf("invalid call result = %#v, error = %v", result, err)
	}
	identity, _ := json.Marshal([]string{account, cloud})
	events, err := audits.List(ctx, string(identity), 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("audit events = %#v, err = %v", events, err)
	}
	successes := 0
	for _, event := range events {
		if event.Tool != "jira_search" {
			t.Fatalf("unexpected audit event = %#v", event)
		}
		if event.Success {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected one success and one failure: %#v", events)
	}
	other, err := audits.List(ctx, string(identity)+"-other", 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("unexpected other identity events: %#v, %v", other, err)
	}
}
