package jira

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRateLimitReadsOnly(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, after string
		calls                     int
	}{
		{"read", "GET", "/issue/A-1", "0", 2},
		{"jql read", "POST", "/search/jql", "0", 2},
		{"create", "POST", "/issue", "0", 1},
		{"comment", "POST", "/issue/A-1/comment", "0", 1},
		{"wait too long", "GET", "/issue/A-1", "6", 1},
		{"invalid delay", "GET", "/issue/A-1", "unknown", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method == "POST" && string(body) != `{"test":true}` {
					t.Errorf("wrong request body %q", body)
				}
				count++
				w.Header().Set("Retry-After", tc.after)
				w.WriteHeader(429)
			}))
			defer api.Close()
			client := NewClient(Config{BaseURL: api.URL, BearerToken: "fixture"})
			var body any
			if tc.method == "POST" {
				body = map[string]bool{"test": true}
			}
			err := client.doJSON(tc.method, client.apiPath(tc.path), nil, body, nil)
			if count != tc.calls || err == nil || !strings.Contains(err.Error(), "Retry-After: "+tc.after) {
				t.Fatalf("count=%d err=%v", count, err)
			}
		})
	}
}
