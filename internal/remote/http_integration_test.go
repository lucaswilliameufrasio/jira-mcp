//go:build integration

package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

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
