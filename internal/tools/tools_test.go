package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jira-mcp/internal/jira"
)

func TestRichCommentMentionAndServerRejection(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Body map[string]interface{} `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(body.Body)
		if !strings.Contains(string(encoded), `"type":"mention"`) || !strings.Contains(string(encoded), `"id":"account-123"`) {
			t.Errorf("mention lost: %s", encoded)
		}
		if r.Method == http.MethodPut {
			_, _ = w.Write([]byte(`{"id":"1","body":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	adf := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"account-123","text":"@Example"}}]}]}`
	client := jira.NewClient(jira.Config{BaseURL: server.URL, Deployment: jira.DeploymentCloud, BearerToken: "test-token"})
	if _, err := handleAddComment(client)(json.RawMessage(`{"issue_key":"TEST-1","comment":` + adf + `}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := handleUpdateComment(client)(json.RawMessage(`{"issue_key":"TEST-1","comment_id":"1","comment":` + adf + `}`)); err != nil {
		t.Fatal(err)
	}
	serverClient := jira.NewClient(jira.Config{BaseURL: server.URL, Deployment: jira.DeploymentServer})
	if _, err := handleAddComment(serverClient)(json.RawMessage(`{"issue_key":"TEST-1","comment":` + adf + `}`)); err == nil {
		t.Fatal("expected Server/Data Center to reject ADF")
	}
	if _, err := handleAddComment(client)(json.RawMessage(`{"issue_key":"TEST-1","comment":{"type":"doc"}}`)); err == nil {
		t.Fatal("expected invalid ADF to fail")
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestDeleteIssueRequiresConfirmation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodDelete || r.URL.Path != "/rest/api/3/issue/TEST-1" || r.URL.RawQuery != "" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := jira.NewClient(jira.Config{BaseURL: server.URL, Deployment: jira.DeploymentCloud, BearerToken: "test-token"})
	if _, err := handleDeleteIssue(client)(json.RawMessage(`{"issue_key":"TEST-1"}`)); err == nil {
		t.Fatal("delete without confirmation succeeded")
	}
	if _, err := handleDeleteIssue(client)(json.RawMessage(`{"issue_key":"TEST-1","confirm":true}`)); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestListIssueChildrenUsesParentJQL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["jql"] != `parent = "TEST-1"` || body["nextPageToken"] != "next" {
			t.Fatalf("search request = %#v", body)
		}
		_, _ = w.Write([]byte(`{"issues":[{"key":"TEST-2","fields":{"summary":"Child"}}],"isLast":true}`))
	}))
	defer server.Close()
	client := jira.NewClient(jira.Config{BaseURL: server.URL, Deployment: jira.DeploymentCloud, BearerToken: "test-token"})
	got, err := handleListIssueChildren(client)(json.RawMessage(`{"parent_key":"TEST-1","page_token":"next"}`))
	if err != nil || !strings.Contains(got, "TEST-2") {
		t.Fatalf("result %q, error %v", got, err)
	}
}

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

func TestHandleUpdateIssueAcceptsADFForDescriptionAndLongTextCustomFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fields := map[string]jira.FieldMetadata{
				"description":       {Name: "Description", Schema: map[string]interface{}{"type": "string"}, Operations: []string{"set"}},
				"customfield_10073": {Name: "Acceptance criteria", Schema: map[string]interface{}{"type": "string"}, Operations: []string{"set"}},
				"customfield_10232": {Name: "Details", Schema: map[string]interface{}{"type": "string"}, Operations: []string{"set"}},
			}
			_ = json.NewEncoder(w).Encode(jira.EditMetadata{Fields: fields})
			return
		}
		var body map[string]map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"description", "customfield_10073", "customfield_10232"} {
			value, ok := body["fields"][key].(map[string]interface{})
			if !ok || value["type"] != "doc" {
				t.Errorf("fields[%q] = %#v, want ADF doc", key, body["fields"][key])
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := jira.NewClient(jira.Config{BaseURL: server.URL, Deployment: jira.DeploymentCloud, BearerToken: "token"})
	input := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"custom text"}]}]}`
	got, err := handleUpdateIssue(client)(json.RawMessage(`{"issue_key":"TEST-1","fields":{"description":"updated plain text"},"custom_fields":{"customfield_10073":` + input + `,"customfield_10232":` + input + `}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "atualizada") {
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

func TestAvailableToolNamesIncludesIssueRankTools(t *testing.T) {
	available := AvailableToolNames()
	for _, want := range []string{"jira_get_issue_rank", "jira_update_issue_rank"} {
		found := false
		for _, name := range available {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("available tools do not include %q: %v", want, available)
		}
	}
}

func assignTestServer(t *testing.T, users string, assigned *map[string]interface{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/user/assignable/search":
			if r.URL.Query().Get("issueKey") != "ABC-1" {
				t.Errorf("issueKey = %q", r.URL.Query().Get("issueKey"))
			}
			_, _ = w.Write([]byte(users))
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself":
			_, _ = w.Write([]byte(`{"accountId":"acc-me","displayName":"Eu Mesmo","active":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/rest/api/3/issue/ABC-1/assignee":
			if err := json.NewDecoder(r.Body).Decode(assigned); err != nil {
				t.Fatal(err)
			}
			if id, _ := (*assigned)["accountId"].(string); id == "ghost" {
				http.Error(w, `{"errorMessages":["user not found"]}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHandleAssignIssueResolvesAssignee(t *testing.T) {
	const twoLucas = `[{"accountId":"acc-1","displayName":"Lucas Eufrasio","active":true},{"accountId":"acc-2","displayName":"Lucas Eufrasio Silva","active":true}]`
	cases := []struct {
		name, users, assignee string
		wantID                interface{}
		wantOut               string
	}{
		{"single match by name", `[{"accountId":"acc-1","displayName":"Lucas Eufrasio","active":true}]`, "lucas", "acc-1", "Lucas Eufrasio (accountId=acc-1)"},
		{"exact name wins over partial", twoLucas, "lucas eufrasio", "acc-1", "accountId=acc-1"},
		{"raw accountId passes through", `[]`, "712020:abc", "712020:abc", "712020:abc"},
		{"me", `[]`, "me", "acc-me", "Eu Mesmo (accountId=acc-me)"},
		{"unassign", `[]`, "unassign", nil, "atribuição removida"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assigned := map[string]interface{}{}
			server := assignTestServer(t, tc.users, &assigned)
			client := jira.NewClient(jira.Config{BaseURL: server.URL, Email: "a@b.c", APIToken: "t"})
			out, err := handleAssignIssue(client)(json.RawMessage(`{"issue_key":"ABC-1","assignee":"` + tc.assignee + `"}`))
			if err != nil {
				t.Fatal(err)
			}
			got, present := assigned["accountId"]
			if !present || got != tc.wantID {
				t.Errorf("accountId sent = %v (present=%v), want %v", got, present, tc.wantID)
			}
			if !strings.Contains(out, tc.wantOut) {
				t.Errorf("output %q missing %q", out, tc.wantOut)
			}
		})
	}
}

func TestHandleAssignIssueRejectsAmbiguousAndUnknownNames(t *testing.T) {
	assigned := map[string]interface{}{}
	server := assignTestServer(t, `[{"accountId":"acc-1","displayName":"Lucas A","active":true},{"accountId":"acc-2","displayName":"Lucas B","active":true}]`, &assigned)
	client := jira.NewClient(jira.Config{BaseURL: server.URL, Email: "a@b.c", APIToken: "t"})
	_, err := handleAssignIssue(client)(json.RawMessage(`{"issue_key":"ABC-1","assignee":"lucas"}`))
	if err == nil || !strings.Contains(err.Error(), "acc-1") || !strings.Contains(err.Error(), "acc-2") {
		t.Fatalf("ambiguous name should list candidates, got %v", err)
	}
	if len(assigned) != 0 {
		t.Errorf("ambiguous name must not assign, sent %v", assigned)
	}

	server = assignTestServer(t, `[]`, &assigned)
	client = jira.NewClient(jira.Config{BaseURL: server.URL, Email: "a@b.c", APIToken: "t"})
	_, err = handleAssignIssue(client)(json.RawMessage(`{"issue_key":"ABC-1","assignee":"ghost"}`))
	if err == nil || !strings.Contains(err.Error(), "jira_search_users") {
		t.Fatalf("unknown user should point to jira_search_users, got %v", err)
	}
}

func TestHandleSearchUsersListsIdentifiers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/user/search" || r.URL.Query().Get("query") != "lucas" || r.URL.Query().Get("maxResults") != "20" {
			t.Errorf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`[{"accountId":"acc-1","displayName":"Lucas Eufrasio","emailAddress":"l@example.com","active":true},{"accountId":"acc-9","displayName":"Lucas Antigo","active":false}]`))
	}))
	defer server.Close()
	client := jira.NewClient(jira.Config{BaseURL: server.URL, Email: "a@b.c", APIToken: "t"})
	out, err := handleSearchUsers(client)(json.RawMessage(`{"query":"lucas"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Lucas Eufrasio (accountId=acc-1) <l@example.com>", "Lucas Antigo (accountId=acc-9) [inativo]"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
	if _, err := handleSearchUsers(client)(json.RawMessage(`{}`)); err == nil {
		t.Error("expected an error without query or issue_key")
	}
}

func TestFormatIssueShowsAssigneeIdentifier(t *testing.T) {
	out := formatIssue(&jira.Issue{Key: "ABC-1", Fields: map[string]interface{}{
		"assignee": map[string]interface{}{"displayName": "Lucas Eufrasio", "accountId": "acc-1"},
	}})
	if !strings.Contains(out, "Responsável: Lucas Eufrasio (accountId=acc-1)") {
		t.Errorf("assignee id missing: %s", out)
	}
}
