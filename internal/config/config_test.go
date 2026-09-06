package config

import (
	"os"
	"strings"
	"testing"

	"jira-mcp/internal/jira"
)

func TestToJiraConfigCloudNormalizesURL(t *testing.T) {
	cfg, err := toJiraConfig(File{BaseURL: " https://jira.example/ ", Deployment: "cloud", Email: " user@example.com ", APIToken: " token "})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://jira.example" || cfg.Deployment != jira.DeploymentCloud || cfg.Email != "user@example.com" || cfg.APIToken != "token" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestToJiraConfigRejectsMissingCredentials(t *testing.T) {
	tests := []File{
		{BaseURL: "https://jira.example", Deployment: "cloud", Email: "user@example.com"},
		{BaseURL: "https://jira.example", Deployment: "server"},
		{BaseURL: "https://jira.example", Deployment: "unknown"},
	}
	for _, input := range tests {
		if _, err := toJiraConfig(input); err == nil {
			t.Fatalf("expected error for %+v", input)
		}
	}
}

func TestParseSelectionDeduplicatesAndRejectsInvalidValues(t *testing.T) {
	choices := []string{"one", "two", "three"}
	selected, err := parseSelection("2, 1,2", choices)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selected, ",") != "two,one" {
		t.Fatalf("selected = %v", selected)
	}
	if _, err := parseSelection("0", choices); err == nil {
		t.Fatal("expected invalid selection error")
	}
	if _, err := parseSelection("four", choices); err == nil {
		t.Fatal("expected non-numeric selection error")
	}
}

func TestRunSetupWritesPrivateConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	input := strings.NewReader("https://jira.example\ncloud\nuser@example.com\nsecret\nall\n")
	if err := RunSetup(input, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions = %o", info.Mode().Perm())
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIToken != "secret" || len(loaded.Tools) == 0 {
		t.Fatalf("unexpected saved config: %+v", loaded)
	}
}
