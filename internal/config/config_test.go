package config

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jira-mcp/internal/jira"
	"jira-mcp/pkg/selection"
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
	selected, err := selection.Parse("2, 1,2", choices)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selected, ",") != "two,one" {
		t.Fatalf("selected = %v", selected)
	}
	if _, err := selection.Parse("0", choices); err == nil {
		t.Fatal("expected invalid selection error")
	}
	if _, err := selection.Parse("four", choices); err == nil {
		t.Fatal("expected non-numeric selection error")
	}
}

func TestParseSelectionAcceptsNamesAndMixedInput(t *testing.T) {
	choices := []string{"one", "two", "three"}
	selected, err := selection.Parse("two, one", choices)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selected, ",") != "two,one" {
		t.Fatalf("names: selected = %v", selected)
	}
	selected, err = selection.Parse("1,three", choices)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selected, ",") != "one,three" {
		t.Fatalf("mixed: selected = %v", selected)
	}
}

func TestParseSelectionKeepsCurrentSelectionOnEnter(t *testing.T) {
	current := []string{"two", "three"}
	joined := strings.Join(current, ",")
	selected, err := selection.Parse(joined, []string{"one", "two", "three", "four"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selected, ",") != joined {
		t.Fatalf("enter kept selection = %v, want %v", selected, current)
	}
}

func TestRunSetupWritesPrivateConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	input := strings.NewReader("https://jira.example\ncloud\nuser@example.com\nsecret\n\n")
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

func TestRunSetupKeepsToolsOnEnter(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	seed := File{BaseURL: "https://jira.example", Deployment: "cloud", Email: "old@example.com", APIToken: "old-token", Tools: []string{"jira_get_issue", "jira_search"}}
	if err := os.MkdirAll(filepath.Dir(Path()), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), b, 0600); err != nil {
		t.Fatal(err)
	}

	// Re-run editing only the email; pressing Enter echoes the prompt default
	// (the full joined tool list), which must parse back unchanged.
	joined := strings.Join(seed.Tools, ",")
	input := strings.NewReader("https://jira.example\ncloud\nnew@example.com\nnew-token\n\n")
	if err := RunSetup(input, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Email != "new@example.com" || loaded.APIToken != "new-token" {
		t.Fatalf("credentials not updated: %+v", loaded)
	}
	if strings.Join(loaded.Tools, ",") != joined {
		t.Fatalf("tools not preserved: %v", loaded.Tools)
	}
}

func TestConfigureToolsReplacesSelectionInOnePrompt(t *testing.T) {
	choices := []string{"one", "two", "three"}
	selected, err := configureTools(bufio.NewReader(strings.NewReader("two,three\n")), &strings.Builder{}, []string{"one", "two"}, choices)
	if err != nil || strings.Join(selected, ",") != "two,three" {
		t.Fatalf("selected = %v, err = %v", selected, err)
	}
}

func TestResolveReadsToolsFromEnvironment(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://jira.example")
	t.Setenv("JIRA_EMAIL", "user@example.com")
	t.Setenv("JIRA_API_TOKEN", "secret")
	t.Setenv("JIRA_MCP_TOOLS", "one, two,")
	_, enabled, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !enabled["one"] || !enabled["two"] || len(enabled) != 2 {
		t.Fatalf("unexpected enabled tools: %v", enabled)
	}
}

func TestActiveProfileFileSelectsEnvironmentProfile(t *testing.T) {
	t.Setenv("JIRA_MCP_PROFILE", "secondary.example")
	file := File{
		ActiveProfile: "primary.example",
		Profiles: map[string]Profile{
			"primary.example":   {BaseURL: "https://primary.example", Deployment: "cloud", Email: "primary", APIToken: "one", Tools: []string{"jira_search"}},
			"secondary.example": {BaseURL: "https://secondary.example", Deployment: "cloud", Email: "secondary", APIToken: "two", Tools: []string{"jira_get_issue"}},
		},
	}
	selected := activeProfileFile(file)
	if selected.BaseURL != "https://secondary.example" || selected.Email != "secondary" || strings.Join(selected.Tools, ",") != "jira_get_issue" {
		t.Fatalf("selected profile = %+v", selected)
	}
}

func TestSaveProfileDerivesNameFromHost(t *testing.T) {
	file := saveProfile(File{BaseURL: "https://jira.example/", Deployment: "cloud", Tools: []string{"jira_search"}})
	profile, ok := file.Profiles["jira.example"]
	if !ok || file.ActiveProfile != "jira.example" || profile.BaseURL != "https://jira.example/" {
		t.Fatalf("file = %+v", file)
	}
}

func TestAddToOpenCodePreservesConfigAndWritesMCPServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","mcp":{"other":{"enabled":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}

	err := addToOpenCode(path, File{
		BaseURL:    "https://jira.example",
		Deployment: "cloud",
		Email:      "user@example.com",
		APIToken:   "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(b, &config); err != nil {
		t.Fatal(err)
	}
	if config["theme"] != "dark" {
		t.Fatalf("existing config was not preserved: %v", config)
	}
	mcp := config["mcp"].(map[string]any)
	jira := mcp["jira"].(map[string]any)
	if jira["type"] != "local" || jira["enabled"] != true {
		t.Fatalf("unexpected jira server: %v", jira)
	}
	environment := jira["environment"].(map[string]any)
	if environment["JIRA_API_TOKEN"] != "secret" {
		t.Fatalf("credentials were not written: %v", environment)
	}
}
