//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"jira-mcp/internal/audit"
)

type consoleLogCapture struct {
	mu   sync.Mutex
	text string
}

func (c *consoleLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text += string(p)
	return len(p), nil
}

func (c *consoleLogCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text
}

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
	stateDir := t.TempDir()
	cmd.Env = append(os.Environ(), "JIRA_BASE_URL="+fakeJira.URL, "JIRA_DEPLOYMENT=cloud", "JIRA_EMAIL=e2e@example.com", "JIRA_API_TOKEN=e2e-token", "XDG_CONFIG_HOME="+t.TempDir(), "XDG_STATE_HOME="+stateDir, "JIRA_MCP_WEB=true")
	logs := &consoleLogCapture{}
	cmd.Stderr = logs
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
	store, err := audit.NewFileStore(filepath.Join(stateDir, "jira-mcp", "audit"), audit.DefaultRetention)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.List(ctx, "local", 10)
	if err != nil || len(events) != 1 || events[0].Tool != "jira_search" || !events[0].Success {
		t.Fatalf("audit events = %#v, err = %v", events, err)
	}
	stderr := logs.String()
	if strings.Contains(stderr, "local console: http") {
		t.Fatalf("stdio process exposed a console URL: %s", stderr)
	}
	if !strings.Contains(stderr, "JIRA_MCP_WEB no longer starts the panel") {
		t.Fatalf("stdio process did not ignore JIRA_MCP_WEB: %s", stderr)
	}
	console := exec.Command(binary, "console")
	console.Env = append(os.Environ(), "XDG_STATE_HOME="+stateDir)
	consoleOut, err := console.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := console.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if console.Process != nil {
			_ = console.Process.Kill()
		}
		_ = console.Wait()
	})
	addressCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(consoleOut)
		for scanner.Scan() {
			const prefix = "[jira-mcp] local console: "
			line := scanner.Text()
			if strings.HasPrefix(line, prefix) {
				addressCh <- strings.TrimPrefix(line, prefix)
				return
			}
		}
	}()
	var address string
	select {
	case address = <-addressCh:
	case <-ctx.Done():
		t.Fatal("local console address missing from jira-mcp console")
	}
	if !strings.HasPrefix(address, "http://127.0.0.1:") {
		t.Fatal("console did not bind to loopback")
	}
	base := strings.TrimSuffix(address, "/web/overview")
	for _, path := range []string{"/web/overview", "/web/monitoring", "/web/audit"} {
		response, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		_, err = body.ReadFrom(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(body.String(), "Modo local") {
			t.Fatalf("local page %s failed", path)
		}
		if path == "/web/audit" && !strings.Contains(body.String(), "jira_search") {
			t.Fatal("real MCP event not shown in local console")
		}
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
