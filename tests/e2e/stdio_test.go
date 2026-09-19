//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBinaryVersionExitsImmediately(t *testing.T) {
	binary := os.Getenv("JIRA_MCP_BINARY")
	if binary == "" {
		t.Skip("JIRA_MCP_BINARY is required for e2e tests")
	}

	cmd := exec.Command(binary, "--version")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("jira-mcp --version did not exit")
	}

	if got := strings.TrimSpace(output.String()); got != "jira-mcp dev" {
		t.Fatalf("jira-mcp --version = %q, want %q", got, "jira-mcp dev")
	}
}

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
