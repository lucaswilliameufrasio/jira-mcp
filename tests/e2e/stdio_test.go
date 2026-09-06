//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBinarySpeaksMCPOverStdio(t *testing.T) {
	binary := os.Getenv("JIRA_MCP_BINARY")
	if binary == "" {
		t.Skip("JIRA_MCP_BINARY is required for e2e tests")
	}
	fakeJira := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"issues": []map[string]any{{"key": "E2E-1", "fields": map[string]any{"summary": "e2e"}}}, "isLast": true})
	}))
	defer fakeJira.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "JIRA_BASE_URL="+fakeJira.URL, "JIRA_DEPLOYMENT=cloud", "JIRA_EMAIL=e2e@example.com", "JIRA_API_TOKEN=e2e-token", "XDG_CONFIG_HOME="+t.TempDir())
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTool(listed.Tools, "jira_search") {
		t.Fatal("jira_search is not exposed")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "jira_search", Arguments: map[string]any{"jql": "project = E2E"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) == 0 {
		t.Fatalf("tool result = %+v", result)
	}
}

func hasTool(tools []*mcp.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
