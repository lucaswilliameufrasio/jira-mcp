//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type requestRecord struct {
	Method string
	Path   string
	Auth   string
	Body   []byte
}

type fakeJira struct {
	server  *httptest.Server
	mu      sync.Mutex
	records []requestRecord
}

func newFakeJira() *fakeJira {
	f := &fakeJira{}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeJira) close() { f.server.Close() }

func (f *fakeJira) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.records = append(f.records, requestRecord{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: body})
	f.mu.Unlock()

	if r.URL.Path == "/rest/api/3/search/jql" && r.Method == http.MethodPost {
		var in struct {
			JQL string `json:"jql"`
		}
		if json.Unmarshal(body, &in) != nil || in.JQL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errorMessages": []string{"jql is required"}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"issues":        []map[string]any{{"id": "10001", "key": "TEST-1", "fields": map[string]any{"summary": "contract issue"}}},
			"nextPageToken": "next-token",
			"isLast":        false,
		})
		return
	}
	if r.URL.Path == "/rest/api/2/search" && r.Method == http.MethodGet {
		if r.URL.Query().Get("jql") == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errorMessages": []string{"jql is required"}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"startAt": 0, "maxResults": 1, "total": 1,
			"issues": []map[string]any{{"id": "10001", "key": "TEST-1", "fields": map[string]any{"summary": "contract issue"}}},
		})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/issue/createmeta") && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"projects": []map[string]any{{
				"key": "TEST",
				"issuetypes": []map[string]any{{
					"name": "Task",
					"fields": map[string]any{
						"project":   map[string]any{"required": true, "name": "Project", "schema": map[string]any{"type": "project"}},
						"summary":   map[string]any{"required": true, "name": "Summary", "schema": map[string]any{"type": "string"}},
						"issuetype": map[string]any{"required": true, "name": "Issue Type", "schema": map[string]any{"type": "issuetype"}},
					},
				}},
			}},
		})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/issue") && r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/rest/api/") {
		var in struct {
			Fields map[string]any `json:"fields"`
		}
		if json.Unmarshal(body, &in) != nil || in.Fields["summary"] == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errorMessages": []string{"invalid issue fields"}})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/rest/api/3/") {
			if _, ok := in.Fields["description"].(map[string]any); !ok {
				writeJSON(w, http.StatusBadRequest, map[string]any{"errors": map[string]string{"description": "ADF required"}})
				return
			}
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": "10002", "key": "TEST-2", "self": f.server.URL + r.URL.Path + "/10002"})
		return
	}
	if r.URL.Path == "/rest/agile/1.0/board" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"isLast": true, "maxResults": 1, "startAt": 0, "total": 1,
			"values": []map[string]any{{"id": 42, "name": "Contract board", "type": "scrum", "location": map[string]any{"projectKey": "TEST"}}},
		})
		return
	}

	if r.URL.Path == "/rest/api/3/myself" || r.URL.Path == "/rest/api/2/myself" {
		writeJSON(w, http.StatusOK, map[string]any{"accountId": "account-1", "displayName": "Contract User"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"errorMessages": []string{"route not in Jira contract fixture"}})
}

func (f *fakeJira) recordsFor(path string) []requestRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []requestRecord
	for _, record := range f.records {
		if record.Path == path {
			out = append(out, record)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type mcpProcess struct {
	name string
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Scanner
	next int
}

func startMCP(ctx context.Context, name, binary, baseURL, deployment string) (*mcpProcess, error) {
	cmd := exec.CommandContext(ctx, binary)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "JIRA_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "JIRA_BASE_URL="+baseURL, "JIRA_URL="+baseURL, "JIRA_DEPLOYMENT="+deployment)
	if deployment == "cloud" {
		cmd.Env = append(cmd.Env, "JIRA_EMAIL=contract@example.com", "JIRA_API_TOKEN=cloud-token")
	} else {
		cmd.Env = append(cmd.Env, "JIRA_PERSONAL_ACCESS_TOKEN=server-token", "JIRA_USERNAME=contract-user", "JIRA_PERSONAL_TOKEN=server-token")
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &mcpProcess{name: name, cmd: cmd, in: in, out: bufio.NewScanner(out), next: 1}, nil
}

func (p *mcpProcess) close() error {
	_ = p.in.Close()
	return p.cmd.Wait()
}

func (p *mcpProcess) call(method string, params any) (map[string]any, error) {
	id := p.next
	p.next++
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	b, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(p.in, "%s\n", b); err != nil {
		return nil, err
	}
	if !p.out.Scan() {
		return nil, fmt.Errorf("%s stopped before replying to %s: %v", p.name, method, p.out.Err())
	}
	var response map[string]any
	if err := json.Unmarshal(p.out.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("%s returned invalid JSON: %w", p.name, err)
	}
	if response["error"] != nil {
		return nil, fmt.Errorf("%s returned RPC error for %s: %v", p.name, method, response["error"])
	}
	return response, nil
}

func validate(binary, deployment string) error {
	fake := newFakeJira()
	defer fake.close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	name := binary
	p, err := startMCP(ctx, name, binary, fake.server.URL, deployment)
	if err != nil {
		return err
	}
	defer p.close()
	if _, err := p.call("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "validation", "version": "1"},
	}); err != nil {
		return err
	}
	if _, err := p.call("tools/list", map[string]any{}); err != nil {
		return err
	}
	if _, err := p.call("tools/call", map[string]any{"name": "jira_search", "arguments": map[string]any{"jql": "project = TEST"}}); err != nil {
		return err
	}
	if _, err := p.call("tools/call", map[string]any{"name": "jira_create_issue", "arguments": map[string]any{
		"project_key": "TEST", "issue_type": "Task", "summary": "contract test", "description": "created by contract test",
	}}); err != nil {
		return err
	}
	if _, err := p.call("tools/call", map[string]any{"name": "jira_list_boards", "arguments": map[string]any{"project_key": "TEST"}}); err != nil {
		return err
	}

	searchPath := "/rest/api/3/search/jql"
	if deployment == "server" {
		searchPath = "/rest/api/2/search"
	}
	search := fake.recordsFor(searchPath)
	if len(search) == 0 {
		return fmt.Errorf("%s did not call the documented search endpoint %s", name, searchPath)
	}
	for _, record := range search {
		if deployment == "server" && record.Auth != "Bearer server-token" {
			return fmt.Errorf("%s uses %q instead of Bearer auth for Data Center", name, record.Auth)
		}
		if deployment == "cloud" {
			want := "Basic " + base64.StdEncoding.EncodeToString([]byte("contract@example.com:cloud-token"))
			if record.Auth != want {
				return fmt.Errorf("%s sent unexpected Cloud auth %q", name, record.Auth)
			}
		}
	}
	if len(fake.recordsFor("/rest/agile/1.0/board")) == 0 {
		return fmt.Errorf("%s did not call the documented Agile board endpoint", name)
	}
	return nil
}

func benchmark(binary string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fake := newFakeJira()
	defer fake.close()
	p, err := startMCP(ctx, binary, binary, fake.server.URL, "cloud")
	if err != nil {
		return err
	}
	defer p.close()
	if _, err := p.call("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "benchmark", "version": "1"},
	}); err != nil {
		return err
	}
	const iterations = 200
	started := time.Now()
	for i := 0; i < iterations; i++ {
		if _, err := p.call("ping", map[string]any{}); err != nil {
			return err
		}
	}
	elapsed := time.Since(started)
	log.Printf("BENCH %s ping: %d calls, %s total, %s/call", binary, iterations, elapsed.Round(time.Microsecond), (elapsed / iterations).Round(time.Microsecond))
	return nil
}

func TestMCPJiraContract(t *testing.T) {
	binary := os.Getenv("JIRA_MCP_BINARY")
	if binary == "" {
		t.Skip("JIRA_MCP_BINARY is required for MCP contract tests")
	}
	for _, deployment := range []string{"cloud", "server"} {
		t.Run(deployment, func(t *testing.T) {
			if err := validate(binary, deployment); err != nil {
				t.Fatal(err)
			}
		})
	}
}
