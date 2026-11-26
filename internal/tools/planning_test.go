package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"jira-mcp/internal/jira"
	"jira-mcp/internal/mcp"
)

func planningSession(t *testing.T, deployment jira.Deployment, handler http.HandlerFunc) *sdk.ClientSession {
	t.Helper()
	api := httptest.NewServer(handler)
	t.Cleanup(api.Close)
	client := jira.NewClient(jira.Config{BaseURL: api.URL, Deployment: deployment, BearerToken: "fixture", PersonalAccessToken: "fixture"})
	host := httptest.NewServer(mcp.NewHTTPHandler(func(_ *http.Request) *mcp.Server {
		s := mcp.NewServer("jira", "test")
		Register(s, client)
		return s
	}))
	t.Cleanup(host.Close)
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), &sdk.StreamableClientTransport{Endpoint: host.URL, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callPlanning(t *testing.T, session *sdk.ClientSession, tool string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("%s: %v", tool, res.Content[0])
	}
	return res
}

func TestSearchAllPagesAndAgileOffsets(t *testing.T) {
	session := planningSession(t, jira.DeploymentServer, func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		page := jira.SearchResult{StartAt: start, Total: 60}
		for i := start; i < min(start+25, 60); i++ {
			page.Issues = append(page.Issues, jira.Issue{Key: fmt.Sprintf("A-%d", i+1)})
		}
		_ = json.NewEncoder(w).Encode(page)
	})
	start := 0
	for _, count := range []int{25, 25, 10} {
		res := callPlanning(t, session, "jira_search", map[string]any{"start_at": start, "jql": "project=A"})
		page := res.StructuredContent.(map[string]any)
		issues := page["issues"].([]any)
		if len(issues) != count || issues[0].(map[string]any)["key"] != fmt.Sprintf("A-%d", start+1) {
			t.Fatal(page)
		}
		start += count
		if start < 60 && page["next_start_at"] != float64(start) || start == 60 && page["next_start_at"] != nil {
			t.Fatal(page)
		}
	}
	for _, kind := range []string{"board", "sprint"} {
		res := callPlanning(t, session, "jira_"+kind+"_issues", map[string]any{kind + "_id": 3, "start_at": 25})
		if !strings.Contains(res.Content[0].(*sdk.TextContent).Text, "start_at=50") {
			t.Fatal(res.Content)
		}
	}
}

func TestCancelSearchStopsJira(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	session := planningSession(t, jira.DeploymentCloud, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			close(stopped)
		case <-time.After(5 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		_, _ = session.CallTool(ctx, &sdk.CallToolParams{Name: "jira_search", Arguments: map[string]any{"jql": "project=A"}})
	}()
	select {
	case <-started:
		cancel()
	case <-ctx.Done():
		t.Fatal("search did not reach Jira")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Jira request did not stop")
	}
}

func TestUploadThenDownloadOriginal(t *testing.T) {
	for filename, original := range map[string][]byte{"sample.pdf": append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte(" "), 4<<20)...), "sample.png": {137, 80, 78, 71, 13, 10, 26, 10}} {
		t.Run(filename, func(t *testing.T) {
			var stored []byte
			session := planningSession(t, jira.DeploymentCloud, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/rest/api/3/issue/A-1/attachments":
					f, h, err := r.FormFile("file")
					if err != nil {
						t.Error(err)
						return
					}
					defer func() { _ = f.Close() }()
					if h.Filename != filename || r.Header.Get("X-Atlassian-Token") != "no-check" {
						t.Error("invalid multipart upload")
					}
					stored, err = io.ReadAll(f)
					if err != nil {
						t.Error(err)
					}
					_, _ = fmt.Fprintf(w, `[{"id":"42","filename":%q,"size":%d}]`, filename, len(stored))
				case "/rest/api/3/issue/A-1":
					_, _ = fmt.Fprintf(w, `{"fields":{"attachment":[{"id":"42","filename":%q,"size":%d}]}}`, filename, len(stored))
				case "/rest/api/3/attachment/content/42":
					_, _ = w.Write(stored)
				default:
					http.NotFound(w, r)
				}
			})
			upload := callPlanning(t, session, "jira_upload_attachment", map[string]any{"issue_key": "A-1", "filename": filename, "data": base64.StdEncoding.EncodeToString(original)})
			if !strings.Contains(upload.Content[0].(*sdk.TextContent).Text, "ID: 42") {
				t.Fatal(upload.Content)
			}
			got := callPlanning(t, session, "jira_download_attachment", map[string]any{"issue_key": "A-1", "attachment_id": "42"})
			if !bytes.Equal(got.Content[1].(*sdk.EmbeddedResource).Resource.Blob, original) {
				t.Fatal("download differs from upload")
			}
		})
	}
}

func TestMoveAndConfirmSprintThenBacklog(t *testing.T) {
	current := "null"
	writes := 0
	session := planningSession(t, jira.DeploymentCloud, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/agile/1.0/sprint/7/issue", "/rest/agile/1.0/backlog/issue":
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || string(body) != `{"issues":["A-1"]}` {
				t.Errorf("unexpected move: %s %s", r.Method, body)
			}
			writes++
			current = "null"
			if strings.Contains(r.URL.Path, "/sprint/") {
				current = `{"id":7,"name":"Sprint sete"}`
			}
			w.WriteHeader(204)
		case "/rest/agile/1.0/issue/A-1":
			_, _ = fmt.Fprintf(w, `{"fields":{"sprint":%s}}`, current)
		default:
			http.NotFound(w, r)
		}
	})
	for _, dest := range []string{"sprint", "backlog"} {
		args := map[string]any{"issue_key": "A-1", "destination": dest}
		if dest == "sprint" {
			args["sprint_id"] = 7
		}
		res := callPlanning(t, session, "jira_move_issue", args)
		if !strings.Contains(res.Content[0].(*sdk.TextContent).Text, dest) {
			t.Fatal(res.Content)
		}
	}
	if writes != 2 {
		t.Fatalf("%d moves", writes)
	}
}

func TestHistoryFilterKeepsPageCursor(t *testing.T) {
	for _, deployment := range []jira.Deployment{jira.DeploymentCloud, jira.DeploymentServer} {
		t.Run(string(deployment), func(t *testing.T) {
			const first = `{"id":"1","items":[{"field":"summary"}]}`
			const second = `{"id":"2","author":{"displayName":"Ana"},"created":"2026-11-26","items":[{"fieldId":"status","toString":"Done"},{"field":"summary"}]}`
			session := planningSession(t, deployment, func(w http.ResponseWriter, r *http.Request) {
				if deployment == jira.DeploymentServer {
					if r.URL.Query().Get("expand") != "changelog" {
						t.Error("missing expansion")
					}
					_, _ = fmt.Fprintf(w, `{"changelog":{"total":2,"histories":[%s,%s]}}`, first, second)
				} else {
					value := first
					if r.URL.Query().Get("startAt") == "1" {
						value = second
					}
					_, _ = fmt.Fprintf(w, `{"total":2,"values":[%s]}`, value)
				}
			})
			for start := range 2 {
				res := callPlanning(t, session, "jira_get_issue_changelog", map[string]any{"issue_key": "A-1", "field": "status", "start_at": start, "max_results": 1})
				text := res.Content[0].(*sdk.TextContent).Text
				if strings.Contains(text, "summary") || start == 0 && !strings.Contains(text, `"next_start_at":1`) || start == 1 && (!strings.Contains(text, "Done") || !strings.Contains(text, "Ana") || strings.Contains(text, "next_start_at")) {
					t.Fatal(text)
				}
			}
		})
	}
}

func TestDisplayLinkDirectionFromEitherIssue(t *testing.T) {
	session := planningSession(t, jira.DeploymentCloud, func(w http.ResponseWriter, r *http.Request) {
		peer := `"outwardIssue":{"key":"B-2"}`
		if strings.HasSuffix(r.URL.Path, "/B-2") {
			peer = `"inwardIssue":{"key":"A-1"}`
		}
		_, _ = fmt.Fprintf(w, `{"fields":{"issuelinks":[{"type":{"name":"Blocks","inward":"é bloqueada por","outward":"bloqueia"},%s}]}}`, peer)
	})
	for issue, want := range map[string]string{"A-1": "A-1 bloqueia B-2", "B-2": "B-2 é bloqueada por A-1"} {
		res := callPlanning(t, session, "jira_list_issue_links", map[string]any{"issue_key": issue})
		if !strings.Contains(res.Content[0].(*sdk.TextContent).Text, want) {
			t.Fatal(res.Content)
		}
	}
}
