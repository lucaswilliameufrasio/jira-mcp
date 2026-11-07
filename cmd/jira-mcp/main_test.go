package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateManagedInstructionsPreservesUserContentAndReplacesBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	initial := "# Project rules\n\nKeep this text.\n\n<!-- jira-mcp:start -->\nold instructions\n<!-- jira-mcp:end -->\n"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	updated := "<!-- jira-mcp:start -->\nnew instructions\n<!-- jira-mcp:end -->\n"
	if err := updateManagedInstructions(path, updated); err != nil {
		t.Fatal(err)
	}
	if err := updateManagedInstructions(path, updated); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "# Project rules") || !strings.Contains(text, "Keep this text.") {
		t.Fatalf("user content was not preserved: %s", text)
	}
	if strings.Count(text, "<!-- jira-mcp:start -->") != 1 || strings.Count(text, "new instructions") != 1 || strings.Contains(text, "old instructions") {
		t.Fatalf("managed section was not replaced idempotently: %s", text)
	}
}

func TestConsoleRejectsArguments(t *testing.T) {
	withArgs(t, []string{"jira-mcp", "console", "extra"})
	err := runConsole()
	if err == nil || !strings.Contains(err.Error(), "does not take arguments") {
		t.Fatalf("runConsole() = %v", err)
	}
}

func TestConsoleRejectsInvalidRetention(t *testing.T) {
	withArgs(t, []string{"jira-mcp", "console"})
	t.Setenv("JIRA_MCP_AUDIT_RETENTION", "nope")
	err := runConsole()
	if err == nil || !strings.Contains(err.Error(), "JIRA_MCP_AUDIT_RETENTION") {
		t.Fatalf("runConsole() = %v", err)
	}
}

func TestConsoleRejectsRelativeStateDirectory(t *testing.T) {
	withArgs(t, []string{"jira-mcp", "console"})
	t.Setenv("JIRA_MCP_AUDIT_RETENTION", "")
	t.Setenv("XDG_STATE_HOME", "relative")
	err := runConsole()
	if err == nil || !strings.Contains(err.Error(), "audit directory unavailable") {
		t.Fatalf("runConsole() = %v", err)
	}
}

func withArgs(t *testing.T, args []string) {
	t.Helper()
	previous := os.Args
	os.Args = args
	t.Cleanup(func() {
		os.Args = previous
	})
}

func TestUpdateManagedInstructionsAppendsToMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := updateManagedInstructions(path, "<!-- jira-mcp:start -->\nJira guidance\n<!-- jira-mcp:end -->\n"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Jira guidance") {
		t.Fatalf("guidance not installed: %s", content)
	}
}
