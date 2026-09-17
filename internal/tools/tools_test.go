package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jira-mcp/internal/jira"
)

func TestFormatIssueIncludesCustomFields(t *testing.T) {
	got := formatIssue(&jira.Issue{
		Key: "TEST-1",
		Fields: map[string]interface{}{
			"summary":           "Example",
			"customfield_10002": "second",
			"customfield_10001": "first",
		},
	})
	if !strings.Contains(got, `customfield_10001: "first"`) || !strings.Contains(got, `customfield_10002: "second"`) {
		t.Fatalf("formatted issue = %q", got)
	}
}

func TestHandleUpdateIssueAcceptsCustomFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(jira.EditMetadata{Fields: map[string]jira.FieldMetadata{
				"customfield_10001": {Name: "Example", Schema: map[string]interface{}{"type": "string"}, Operations: []string{"set"}},
			}})
			return
		}
		var body map[string]map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["fields"]["customfield_10001"] != "value" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := jira.NewClient(jira.Config{BaseURL: server.URL, Deployment: jira.DeploymentCloud, BearerToken: "token"})
	got, err := handleUpdateIssue(client)(json.RawMessage(`{"issue_key":"TEST-1","custom_fields":{"customfield_10001":"value"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "TEST-1") {
		t.Fatalf("result = %q", got)
	}
}

func TestFormatFieldMetadataFiltersCustomFieldsByDefault(t *testing.T) {
	metadata := &jira.EditMetadata{Fields: map[string]jira.FieldMetadata{
		"summary": {
			Name:       "Summary",
			Schema:     map[string]interface{}{"type": "string"},
			Operations: []string{"set"},
		},
		"customfield_10073": {
			Name:       "Acceptance criteria",
			Required:   true,
			Schema:     map[string]interface{}{"type": "string"},
			Operations: []string{"set"},
		},
	}}

	got := formatFieldMetadata("TEST-1", metadata, true)
	if !strings.Contains(got, "customfield_10073: Acceptance criteria") || strings.Contains(got, "Summary") {
		t.Fatalf("formatted metadata = %q", got)
	}
}

func TestHandleGetFieldMetadataCanIncludeStandardFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jira.EditMetadata{Fields: map[string]jira.FieldMetadata{
			"summary":           {Name: "Summary"},
			"customfield_10073": {Name: "Acceptance criteria"},
		}})
	}))
	defer server.Close()

	client := jira.NewClient(jira.Config{BaseURL: server.URL, Deployment: jira.DeploymentCloud, BearerToken: "token"})
	got, err := handleGetFieldMetadata(client)(json.RawMessage(`{"issue_key":"TEST-1","custom_only":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Summary") || !strings.Contains(got, "Acceptance criteria") {
		t.Fatalf("result = %q", got)
	}
}

func TestCommentHandlersFormatAndUpdateComments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/issue/TEST-1/comment/7" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(jira.Comment{ID: "7", Body: map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"text": "hello"}}}}}, Author: jira.CommentUser{DisplayName: "Ana"}})
			return
		}
		if r.URL.Path == "/rest/api/3/issue/TEST-1/comment/7" && r.Method == http.MethodPut {
			_ = json.NewEncoder(w).Encode(jira.Comment{ID: "7", Body: "changed"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := jira.NewClient(jira.Config{BaseURL: server.URL, Deployment: jira.DeploymentCloud, BearerToken: "token"})
	got, err := handleGetComment(client)(json.RawMessage(`{"issue_key":"TEST-1","comment_id":"7"}`))
	if err != nil || !strings.Contains(got, "hello") || !strings.Contains(got, "Ana") {
		t.Fatalf("get result = %q, err = %v", got, err)
	}
	got, err = handleUpdateComment(client)(json.RawMessage(`{"issue_key":"TEST-1","comment_id":"7","comment":"changed"}`))
	if err != nil || !strings.Contains(got, "atualizado") {
		t.Fatalf("update result = %q, err = %v", got, err)
	}
}

func TestHandleUpdateIssueRankRequiresExactlyOneAnchor(t *testing.T) {
	client := jira.NewClient(jira.Config{BaseURL: "http://unused", Deployment: jira.DeploymentCloud, BearerToken: "token"})
	for _, raw := range []string{
		`{"issue_key":"TEST-1"}`,
		`{"issue_key":"TEST-1","before_issue":"TEST-2","after_issue":"TEST-3"}`,
	} {
		if _, err := handleUpdateIssueRank(client)(json.RawMessage(raw)); err == nil {
			t.Fatalf("expected validation error for %s", raw)
		}
	}
}
