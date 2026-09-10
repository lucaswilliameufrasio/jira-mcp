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
