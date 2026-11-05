package console

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"jira-mcp/internal/mcp"
)

func TestPageSummarizesEventsAndEscapesContent(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	events := []mcp.ToolEvent{
		{Tool: "<script>alert(1)</script>", StartedAt: now, Duration: 100 * time.Millisecond, Success: true},
		{Tool: "jira_search", StartedAt: now, Duration: 300 * time.Millisecond, Success: false},
	}
	page := NewPage("audit", "<account>", "site", "csrf", events, false, now)
	if page.Count != 2 || page.Failures != 1 || page.Average != "200ms" {
		t.Fatalf("unexpected summary: count=%d failures=%d average=%s", page.Count, page.Failures, page.Average)
	}
	var out bytes.Buffer
	if err := Render(&out, page); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "<script>alert") || !strings.Contains(out.String(), "&lt;script&gt;") || !strings.Contains(out.String(), "&lt;account&gt;") {
		t.Fatal("page did not escape untrusted values")
	}
}

func TestPageUnavailableDoesNotRenderZeroMetrics(t *testing.T) {
	page := NewPage("overview", "account", "site", "csrf", nil, true, time.Now())
	var out bytes.Buffer
	if err := Render(&out, page); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Dados de auditoria indisponíveis") || strings.Contains(out.String(), "Chamadas na amostra") {
		t.Fatal("unavailable data presented as activity metrics")
	}
}
