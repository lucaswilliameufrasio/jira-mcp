package localconsole

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jira-mcp/internal/audit"
	"jira-mcp/internal/mcp"
)

func TestLocalConsoleReadsAuditAndRejectsCrossOrigin(t *testing.T) {
	store, err := audit.NewFileStore(filepath.Join(t.TempDir(), "audit"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(context.Background(), "local", mcp.ToolEvent{Tool: "jira_search", StartedAt: time.Now(), Duration: 42 * time.Millisecond, Success: true}); err != nil {
		t.Fatal(err)
	}
	server, err := Start(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	client := &http.Client{Timeout: 3 * time.Second}
	base := strings.TrimSuffix(server.URL, "/web/overview")
	for _, path := range []string{"/web/overview", "/web/monitoring", "/web/audit"} {
		response, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Modo local") || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("page %s did not render local data", path)
		}
		if path == "/web/audit" && !strings.Contains(string(body), "jira_search") {
			t.Fatal("audit record missing")
		}
		if path == "/web/monitoring" && !strings.Contains(string(body), "42ms") {
			t.Fatal("duration missing")
		}
		if strings.Contains(string(body), "/web/logout") {
			t.Fatal("local console exposed remote logout")
		}
	}
	for _, fixture := range []struct {
		name   string
		host   string
		origin string
		fetch  string
	}{
		{"rebound host", "evil.example", "", ""},
		{"foreign origin", "", "https://evil.example", ""},
		{"cross-site", "", "", "cross-site"},
		{"same-site other origin", "", "", "same-site"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
			if fixture.host != "" {
				req.Host = fixture.host
			}
			req.Header.Set("Origin", fixture.origin)
			req.Header.Set("Sec-Fetch-Site", fixture.fetch)
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d", response.StatusCode)
			}
		})
	}
	req, _ := http.NewRequest(http.MethodPost, server.URL, nil)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", response.StatusCode)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if response, err := client.Get(server.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("console still accessible after close")
	}
}

func TestLocalConsoleRejectsNonLoopbackPeer(t *testing.T) {
	store, err := audit.NewFileStore(filepath.Join(t.TempDir(), "audit"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/web/overview", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	w := httptest.NewRecorder()
	newHandler(store, "127.0.0.1:1234").ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d", w.Code)
	}
}
