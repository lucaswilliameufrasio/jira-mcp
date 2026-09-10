package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"jira-mcp/internal/jira"
	"jira-mcp/internal/tools"
)

type File struct {
	BaseURL             string   `json:"base_url"`
	Deployment          string   `json:"deployment"`
	Email               string   `json:"email,omitempty"`
	APIToken            string   `json:"api_token,omitempty"`
	PersonalAccessToken string   `json:"personal_access_token,omitempty"`
	BearerToken         string   `json:"-"`
	Tools               []string `json:"tools,omitempty"`
}

func Path() string {
	if root := os.Getenv("XDG_CONFIG_HOME"); root != "" {
		return filepath.Join(root, "jira-mcp", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "jira-mcp", "config.json")
	}
	return filepath.Join(home, ".config", "jira-mcp", "config.json")
}

func Load() (File, error) {
	b, err := os.ReadFile(Path())
	if err != nil {
		return File{}, err
	}
	var cfg File
	if err := json.Unmarshal(b, &cfg); err != nil {
		return File{}, fmt.Errorf("decoding %s: %w", Path(), err)
	}
	return cfg, nil
}

func Resolve() (jira.Config, map[string]bool, error) {
	if os.Getenv("JIRA_BASE_URL") != "" {
		cfg, err := fromEnvironment()
		return cfg, nil, err
	}
	file, err := Load()
	if err != nil {
		return jira.Config{}, nil, fmt.Errorf("configuration not found; run 'jira-mcp setup': %w", err)
	}
	cfg, err := toJiraConfig(file)
	if err != nil {
		return jira.Config{}, nil, err
	}
	return cfg, enabledTools(file.Tools), nil
}

func RunSetup(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	file := File{}
	var err error
	if existing, loadErr := Load(); loadErr == nil {
		file = existing
	}

	file.BaseURL, err = ask(r, out, "Jira URL", file.BaseURL, true)
	if err != nil {
		return err
	}
	deployment, err := ask(r, out, "Deployment (cloud/server)", defaultString(file.Deployment, "cloud"), true)
	if err != nil {
		return err
	}
	file.Deployment = strings.ToLower(deployment)
	if file.Deployment == "cloud" {
		file.Email, err = ask(r, out, "Jira email", file.Email, true)
		if err != nil {
			return err
		}
		file.APIToken, err = ask(r, out, "Jira API token", file.APIToken, true)
	} else {
		file.PersonalAccessToken, err = ask(r, out, "Jira personal access token", file.PersonalAccessToken, true)
	}
	if err != nil {
		return err
	}

	choices := tools.AvailableToolNames()
	_, _ = fmt.Fprintln(out, "\nTools (comma-separated numbers or names, or 'all'):")
	for i, name := range choices {
		_, _ = fmt.Fprintf(out, "  %d. %s\n", i+1, name)
	}
	selection, err := ask(r, out, "Enabled tools", strings.Join(file.Tools, ","), true)
	if err != nil {
		return err
	}
	file.Tools, err = parseSelection(selection, choices)
	if err != nil {
		return err
	}

	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Saved %s\n", path)
	return nil
}

func fromEnvironment() (jira.Config, error) {
	deployment := strings.ToLower(strings.TrimSpace(os.Getenv("JIRA_DEPLOYMENT")))
	if deployment == "" {
		deployment = "cloud"
	}
	return toJiraConfig(File{
		BaseURL:             os.Getenv("JIRA_BASE_URL"),
		Deployment:          deployment,
		Email:               os.Getenv("JIRA_EMAIL"),
		APIToken:            os.Getenv("JIRA_API_TOKEN"),
		PersonalAccessToken: os.Getenv("JIRA_PERSONAL_ACCESS_TOKEN"),
	})
}

func toJiraConfig(file File) (jira.Config, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(file.BaseURL), "/")
	if baseURL == "" {
		return jira.Config{}, errors.New("JIRA_BASE_URL is required")
	}
	cfg := jira.Config{BaseURL: baseURL}
	switch strings.ToLower(strings.TrimSpace(file.Deployment)) {
	case "", "cloud":
		cfg.Deployment = jira.DeploymentCloud
		cfg.Email = strings.TrimSpace(file.Email)
		cfg.APIToken = strings.TrimSpace(file.APIToken)
		if cfg.Email == "" || cfg.APIToken == "" {
			return jira.Config{}, errors.New("jira cloud requires email and API token")
		}
	case "server", "datacenter", "data-center", "dc":
		cfg.Deployment = jira.DeploymentServer
		cfg.PersonalAccessToken = strings.TrimSpace(file.PersonalAccessToken)
		if cfg.PersonalAccessToken == "" {
			return jira.Config{}, errors.New("data center requires a personal access token")
		}
	default:
		return jira.Config{}, fmt.Errorf("invalid deployment %q", file.Deployment)
	}
	return cfg, nil
}

func enabledTools(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result
}

func parseSelection(value string, choices []string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "all" || value == "" {
		return append([]string(nil), choices...), nil
	}
	index := make(map[string]int, len(choices))
	for i, name := range choices {
		index[name] = i
	}
	var selected []string
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			selected = append(selected, name)
			seen[name] = true
		}
	}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if i, ok := index[item]; ok {
			add(choices[i])
			continue
		}
		n, err := strconv.Atoi(item)
		if err != nil || n < 1 || n > len(choices) {
			return nil, fmt.Errorf("invalid tool selection %q", item)
		}
		add(choices[n-1])
	}
	if len(selected) == 0 {
		return nil, errors.New("select at least one tool")
	}
	return selected, nil
}

func ask(r *bufio.Reader, out io.Writer, label, current string, required bool) (string, error) {
	if current != "" {
		_, _ = fmt.Fprintf(out, "%s [%s]: ", label, current)
	} else {
		_, _ = fmt.Fprintf(out, "%s: ", label)
	}
	value, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = current
	}
	if required && value == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	return value, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
