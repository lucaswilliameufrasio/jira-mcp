package jira

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSearchIssuesCloudUsesEnhancedEndpointAndBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.com:secret"))
		if r.Header.Get("Authorization") != want {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["jql"] != "project = TEST" || body["nextPageToken"] != "next" {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(SearchResult{Issues: []Issue{{Key: "TEST-1"}}, IsLast: true})
	}))
	defer server.Close()

	result, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, Email: "user@example.com", APIToken: "secret"}).SearchIssues("project = TEST", 10, nil, "next")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Issues) != 1 || result.Issues[0].Key != "TEST-1" {
		t.Fatalf("result = %+v", result)
	}
}

func TestOAuthBearerTakesPrecedenceOverCloudBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(SearchResult{})
	}))
	defer server.Close()
	_, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, Email: "user@example.com", APIToken: "secret", BearerToken: "oauth-token"}).SearchIssues("project = TEST", 1, nil, "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAPIErrorPreservesStatusAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).SearchIssues("bad", 1, nil, "")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != http.StatusBadRequest || apiErr.Body == "" {
		t.Fatalf("error = %#v", err)
	}
}

func TestGetIssueRequestsAllFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/TEST-1" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("fields"); got != "*all" {
			t.Fatalf("fields = %q", got)
		}
		_ = json.NewEncoder(w).Encode(Issue{Key: "TEST-1", Fields: map[string]interface{}{"customfield_10001": "value"}})
	}))
	defer server.Close()

	issue, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).GetIssue("TEST-1", []string{"*all"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Fields["customfield_10001"] != "value" {
		t.Fatalf("fields = %#v", issue.Fields)
	}
}

func TestUpdateIssueSendsCustomFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/rest/api/3/issue/TEST-1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
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

	err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).UpdateIssue("TEST-1", map[string]interface{}{"customfield_10001": "value"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetFieldMetadataCloudUsesEditMetaEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/TEST-1/editmeta" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"fields":{"customfield_10073":{"required":true,"name":"Acceptance criteria","schema":{"type":"string"},"operations":["set"]}}}`))
	}))
	defer server.Close()

	metadata, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).GetFieldMetadata("TEST-1")
	if err != nil {
		t.Fatal(err)
	}
	field := metadata.Fields["customfield_10073"]
	if field.Name != "Acceptance criteria" || !field.Required || field.Schema["type"] != "string" {
		t.Fatalf("field = %#v", field)
	}
}

func TestGetFieldMetadataServerUsesV2EditMetaEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/2/issue/TEST-1/editmeta" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(EditMetadata{Fields: map[string]FieldMetadata{
			"customfield_10073": {Name: "Acceptance criteria", Operations: []string{"set"}},
		}})
	}))
	defer server.Close()

	metadata, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentServer, PersonalAccessToken: "token"}).GetFieldMetadata("TEST-1")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Fields["customfield_10073"].Name != "Acceptance criteria" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestCommentOperationsUseIssueCommentEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/TEST-1/comment/99" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Method == http.MethodPut {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if _, ok := body["body"].(map[string]any); !ok {
				t.Fatalf("body = %#v", body)
			}
		}
		_ = json.NewEncoder(w).Encode(Comment{ID: "99", Body: "updated"})
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"})
	comment, err := client.GetComment("TEST-1", "99")
	if err != nil || comment.ID != "99" {
		t.Fatalf("get comment = %#v, err = %v", comment, err)
	}
	comment, err = client.UpdateComment("TEST-1", "99", "updated")
	if err != nil || comment.ID != "99" {
		t.Fatalf("update comment = %#v, err = %v", comment, err)
	}
}

func TestListCommentsSendsPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/2/issue/TEST-1/comment" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("startAt") != "10" || r.URL.Query().Get("maxResults") != "5" || r.URL.Query().Get("orderBy") != "-created" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(CommentsPage{StartAt: 10, MaxResults: 5, Total: 11})
	}))
	defer server.Close()

	page, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentServer, PersonalAccessToken: "token"}).ListComments("TEST-1", 5, 10, "-created")
	if err != nil || page.StartAt != 10 || page.Total != 11 {
		t.Fatalf("page = %#v, err = %v", page, err)
	}
}

func TestGetIssueRankPaginatesAndReturnsNeighbors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startAt := r.URL.Query().Get("startAt")
		if startAt == "0" {
			issues := make([]Issue, 50)
			for i := range issues {
				issues[i].Key = "TEST-" + strconv.Itoa(i+1)
			}
			_ = json.NewEncoder(w).Encode(SearchResult{Total: 51, Issues: issues})
			return
		}
		if startAt != "50" {
			t.Fatalf("startAt = %q", startAt)
		}
		_ = json.NewEncoder(w).Encode(SearchResult{Total: 51, Issues: []Issue{{Key: "TEST-51"}}})
	}))
	defer server.Close()

	rank, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).GetIssueRank(42, "TEST-51")
	if err != nil {
		t.Fatal(err)
	}
	if rank.Position != 51 || rank.Total != 51 || rank.PreviousIssue != "TEST-50" || rank.NextIssue != "" {
		t.Fatalf("rank = %+v", rank)
	}
}

func TestUpdateIssueRankUsesRelativeAnchor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/rest/agile/1.0/issue/rank" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["rankBeforeIssue"] != "TEST-2" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).UpdateIssueRank("TEST-1", "TEST-2", ""); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateIssueRankRejectsPartialFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`{"issues":[{"issueId":"10001","errors":["rank failed"]}]}`))
	}))
	defer server.Close()

	err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).UpdateIssueRank("TEST-1", "TEST-2", "")
	if err == nil || !strings.Contains(err.Error(), "partial failure") {
		t.Fatalf("error = %v", err)
	}
}

func TestListBoardsPaginates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var response boardsResponse
		switch r.URL.Query().Get("startAt") {
		case "0":
			response = boardsResponse{Values: []Board{{ID: 1, Name: "First"}}, IsLast: false}
		case "1":
			response = boardsResponse{Values: []Board{{ID: 2, Name: "Second"}}, IsLast: true}
		default:
			t.Fatalf("unexpected startAt = %q", r.URL.Query().Get("startAt"))
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	boards, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).ListBoards("PROJ", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(boards) != 2 || boards[0].ID != 1 || boards[1].ID != 2 {
		t.Fatalf("boards = %#v", boards)
	}
}
