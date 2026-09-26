package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/term"

	"jira-mcp/internal/jira"
	"jira-mcp/internal/setupui"
	"jira-mcp/internal/tools"
	"jira-mcp/pkg/selection"
)

type File struct {
	BaseURL             string             `json:"base_url"`
	Deployment          string             `json:"deployment"`
	Email               string             `json:"email,omitempty"`
	APIToken            string             `json:"api_token,omitempty"`
	PersonalAccessToken string             `json:"personal_access_token,omitempty"`
	BearerToken         string             `json:"-"`
	Tools               []string           `json:"tools,omitempty"`
	ActiveProfile       string             `json:"active_profile,omitempty"`
	Profiles            map[string]Profile `json:"profiles,omitempty"`
}

type Profile struct {
	BaseURL             string   `json:"base_url"`
	Deployment          string   `json:"deployment"`
	Email               string   `json:"email,omitempty"`
	APIToken            string   `json:"api_token,omitempty"`
	PersonalAccessToken string   `json:"personal_access_token,omitempty"`
	Tools               []string `json:"tools,omitempty"`
}

type installTarget struct {
	Name       string
	Path       string
	Format     string
	Configured bool
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
		return cfg, enabledTools(strings.Split(os.Getenv("JIRA_MCP_TOOLS"), ",")), err
	}
	file, err := Load()
	if err != nil {
		return jira.Config{}, nil, fmt.Errorf("configuration not found; run 'jira-mcp setup': %w", err)
	}
	file = activeProfileFile(file)
	cfg, err := toJiraConfig(file)
	if err != nil {
		return jira.Config{}, nil, err
	}
	return cfg, enabledTools(file.Tools), nil
}

func RunSetup(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	_, _ = fmt.Fprintln(out, "Jira MCP setup")
	_, _ = fmt.Fprintln(out, "This wizard saves a local Jira connection and can register it in your AI client.")
	_, _ = fmt.Fprintln(out, "Credentials stay in your local config; never paste them into chat or project files.")
	_, _ = fmt.Fprintln(out, "")
	file := File{}
	var err error
	if existing, loadErr := Load(); loadErr == nil {
		file = activeProfileFile(existing)
	}
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if interactive && len(file.Profiles) > 0 {
		choices := profileChoices(file.Profiles)
		choices = append(choices, "Create new connection")
		current := file.ActiveProfile
		selected, selectErr := setupui.SelectOne(os.Stdin, out, "Select Jira connection", choices, current)
		if selectErr != nil {
			return selectErr
		}
		if selected == "Create new connection" {
			file = newProfileFile(file)
		} else {
			file = profileFile(file, selected)
		}
	}

	_, _ = fmt.Fprintln(out, "Connection details")
	_, _ = fmt.Fprintln(out, "  Jira Cloud: use your site URL (https://company.atlassian.net), Atlassian account email, and an API token.")
	_, _ = fmt.Fprintln(out, "  Create a Cloud API token at https://id.atlassian.com/manage-profile/security/api-tokens")
	_, _ = fmt.Fprintln(out, "  Server/Data Center: use your Jira URL and a Personal Access Token from your Jira profile or administrator.")
	_, _ = fmt.Fprintln(out, "  Press Enter on a saved secret to keep it; entering a value replaces it. Secrets are not shown.")
	file.BaseURL, err = ask(r, out, "Jira site URL (for example https://company.atlassian.net)", file.BaseURL, true)
	if err != nil {
		return err
	}
	deployment, err := ask(r, out, "Jira deployment (cloud or server; Data Center uses server)", defaultString(file.Deployment, "cloud"), true)
	if err != nil {
		return err
	}
	file.Deployment = normalizeDeployment(deployment)
	if file.Deployment == "cloud" {
		file.Email, err = ask(r, out, "Atlassian account email", file.Email, true)
		if err != nil {
			return err
		}
		file.APIToken, err = askSecret(r, out, "Jira Cloud API token", file.APIToken, interactive)
	} else {
		file.PersonalAccessToken, err = askSecret(r, out, "Jira Server/Data Center Personal Access Token", file.PersonalAccessToken, interactive)
	}
	if err != nil {
		return err
	}

	if interactive {
		// Bubble Tea needs the terminal file itself to enable raw mode. Passing
		// the buffered reader here leaves escape sequences visible in the UI.
		_, _ = fmt.Fprintln(out, "\nTools control which Jira operations the AI client can call.")
		_, _ = fmt.Fprintln(out, "Read tools search or inspect Jira; create/update/comment/transition tools can make real changes using this Jira account.")
		_, _ = fmt.Fprintln(out, "Your Jira account permissions still apply. Enable only the operations you want available.")
		file.Tools, err = setupui.SelectTools(os.Stdin, out, tools.AvailableToolNames(), file.Tools)
	} else {
		_, _ = fmt.Fprintln(out, "\nTools control which Jira operations the AI client can call; write tools can make real changes to Jira.")
		file.Tools, err = configureTools(r, out, file.Tools, tools.AvailableToolNames())
	}
	if err != nil {
		return err
	}
	if interactive {
		detectBoards(out, file)
	}

	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file = saveProfile(file)
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "\nSaved connection to %s (permissions: owner read/write only).\n", path)

	targets := detectedInstallTargets()
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(out, "No supported MCP client was detected.")
		_, _ = fmt.Fprintln(out, "Install or open an MCP client, then run `jira-mcp install-mcp`, or follow the manual setup guide:")
		_, _ = fmt.Fprintln(out, "https://github.com/lucaswilliameufrasio/jira-mcp#integra%C3%A7%C3%A3o-com-clientes-mcp")
		return nil
	}

	clientChoices := make([]string, len(targets))
	_, _ = fmt.Fprintln(out, "\nChoose where the Jira server should be registered.")
	_, _ = fmt.Fprintln(out, "Choose only the clients you want to use. On first setup, none are preselected; existing registrations stay selected on later runs.")
	_, _ = fmt.Fprintln(out, "Use Space to toggle, a to select all, n to clear, and Enter to confirm. Unselecting an existing client removes jira-mcp from its config.")
	for i, target := range targets {
		clientChoices[i] = target.Name
		status := "not configured"
		if target.Configured {
			status = "configured"
		}
		_, _ = fmt.Fprintf(out, "  %d. %s [%s]\n     %s\n", i+1, target.Name, status, target.Path)
	}
	defaultTargets := configuredTargetSelection(targets)
	var selected []string
	if interactive {
		selected, err = setupui.SelectManyAllowEmpty(os.Stdin, out, "Select MCP clients to configure", clientChoices, configuredTargetNames(targets))
		if err != nil {
			return err
		}
	} else {
		clientSelection, askErr := ask(r, out, "Clients to configure (numbers/names, 'none', or Enter keeps current)", defaultTargets, false)
		if askErr != nil {
			return askErr
		}
		if clientSelection == "" {
			selected = configuredTargetNames(targets)
		} else if strings.EqualFold(strings.TrimSpace(clientSelection), "none") {
			selected = nil
		} else {
			selected, err = selection.Parse(clientSelection, clientChoices)
		}
		if err != nil {
			return err
		}
	}
	selectedNames := make(map[string]bool, len(selected))
	for _, name := range selected {
		selectedNames[name] = true
	}
	for _, target := range targets {
		if selectedNames[target.Name] {
			if err := updateTarget(target, file); err != nil {
				return fmt.Errorf("configure %s: %w", target.Name, err)
			}
			_, _ = fmt.Fprintf(out, "Updated %s\n", target.Path)
		} else if target.Configured {
			if err := removeFromTarget(target); err != nil {
				return fmt.Errorf("remove %s: %w", target.Name, err)
			}
			_, _ = fmt.Fprintf(out, "Removed jira-mcp from %s\n", target.Path)
		}
	}
	if len(selected) == 0 {
		_, _ = fmt.Fprintln(out, "No MCP client is configured. Your Jira connection is saved; run `jira-mcp install-mcp` when you are ready to select a client.")
	} else {
		_, _ = fmt.Fprintln(out, "\nNext steps:")
		_, _ = fmt.Fprintln(out, "  1. Restart the selected AI client (or reload its MCP servers).")
		_, _ = fmt.Fprintln(out, "  2. Confirm that `jira-mcp` is connected and its Jira tools are listed.")
		_, _ = fmt.Fprintln(out, "  3. Ask the agent to search for a known issue, for example `Find PROJ-123 in Jira`.")
		_, _ = fmt.Fprintln(out, "  If it does not connect, run `jira-mcp doctor` and check that the client uses the installed binary.")
	}
	return nil
}

// InstallMCP adds the saved active Jira profile to selected detected MCP clients.
func InstallMCP(in io.Reader, out io.Writer) error {
	file, err := Load()
	if err != nil {
		return fmt.Errorf("no saved Jira connection; run `jira-mcp setup` first: %w", err)
	}
	file = activeProfileFile(file)
	if _, err := toJiraConfig(file); err != nil {
		return fmt.Errorf("saved Jira connection is incomplete; run `jira-mcp setup`: %w", err)
	}
	targets := detectedInstallTargets()
	if len(targets) == 0 {
		return errors.New("no supported MCP clients detected; run `jira-mcp setup` or configure a client manually")
	}
	choices := make([]string, len(targets))
	current := make([]string, 0, len(targets))
	_, _ = fmt.Fprintln(out, "Select the AI clients where Jira MCP should be registered.")
	for index, target := range targets {
		choices[index] = target.Name
		status := "not configured"
		if target.Configured {
			status = "already configured"
			current = append(current, target.Name)
		}
		_, _ = fmt.Fprintf(out, "  %d. %s — %s\n     Config: %s\n", index+1, target.Name, status, target.Path)
	}
	var selected []string
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		selected, err = setupui.SelectManyAllowEmpty(os.Stdin, out, "Select clients (Space toggles, Enter confirms)", choices, current)
	} else {
		reader := bufio.NewReader(in)
		value, askErr := ask(reader, out, "Clients (numbers or names; Enter keeps current)", strings.Join(current, ","), false)
		if askErr != nil {
			return askErr
		}
		if value == "" {
			selected = current
		} else {
			selected, err = selection.Parse(value, choices)
		}
	}
	if err != nil {
		return err
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, name := range selected {
		selectedSet[name] = true
	}
	installed := 0
	for _, target := range targets {
		if !selectedSet[target.Name] {
			continue
		}
		if err := updateTarget(target, file); err != nil {
			return fmt.Errorf("configure %s: %w", target.Name, err)
		}
		_, _ = fmt.Fprintf(out, "Registered Jira MCP in %s\n", target.Path)
		installed++
	}
	if installed == 0 {
		_, _ = fmt.Fprintln(out, "No clients selected; no configuration changed.")
		return nil
	}
	_, _ = fmt.Fprintln(out, "Restart or reload the selected clients to connect.")
	return nil
}

func Doctor(out io.Writer) error {
	_, _ = fmt.Fprintln(out, "Configuration")
	_, enabled, err := Resolve()
	if err != nil {
		_, _ = fmt.Fprintf(out, "  FAIL  %v\n", err)
	} else {
		_, _ = fmt.Fprintf(out, "  OK    %s\n", Path())
	}

	available := tools.AvailableToolNames()
	_, _ = fmt.Fprintf(out, "\nTools (%d available)\n", len(available))
	for _, name := range available {
		status := "disabled"
		if enabled == nil || enabled[name] {
			status = "enabled"
		}
		_, _ = fmt.Fprintf(out, "  %-8s %s\n", status, name)
	}

	_, _ = fmt.Fprintln(out, "\nMCP clients")
	targets := detectedInstallTargets()
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(out, "  No supported clients detected")
	} else {
		for _, target := range targets {
			status := "not configured"
			if target.Configured {
				status = "configured"
			}
			_, _ = fmt.Fprintf(out, "  %-14s %-15s %s\n", status, target.Name, target.Path)
		}
	}
	return err
}

func configuredTargetSelection(targets []installTarget) string {
	var selected []string
	for i, target := range targets {
		if target.Configured {
			selected = append(selected, strconv.Itoa(i+1))
		}
	}
	return strings.Join(selected, ",")
}

func configuredTargetNames(targets []installTarget) []string {
	var selected []string
	for _, target := range targets {
		if target.Configured {
			selected = append(selected, target.Name)
		}
	}
	return selected
}

func activeProfileFile(file File) File {
	if len(file.Profiles) == 0 {
		return file
	}
	name := strings.TrimSpace(os.Getenv("JIRA_MCP_PROFILE"))
	if name == "" {
		name = file.ActiveProfile
	}
	if name == "" {
		for profileName := range file.Profiles {
			name = profileName
			break
		}
	}
	profile, ok := file.Profiles[name]
	if !ok {
		return file
	}
	profile.Tools = append([]string(nil), profile.Tools...)
	file.BaseURL = profile.BaseURL
	file.Deployment = profile.Deployment
	file.Email = profile.Email
	file.APIToken = profile.APIToken
	file.PersonalAccessToken = profile.PersonalAccessToken
	file.Tools = profile.Tools
	file.ActiveProfile = name
	return file
}

func profileChoices(profiles map[string]Profile) []string {
	choices := make([]string, 0, len(profiles))
	for name := range profiles {
		choices = append(choices, name)
	}
	sort.Strings(choices)
	return choices
}

func profileFile(file File, name string) File {
	profile, ok := file.Profiles[name]
	if !ok {
		return file
	}
	file.BaseURL = profile.BaseURL
	file.Deployment = profile.Deployment
	file.Email = profile.Email
	file.APIToken = profile.APIToken
	file.PersonalAccessToken = profile.PersonalAccessToken
	file.Tools = append([]string(nil), profile.Tools...)
	file.ActiveProfile = name
	return file
}

func newProfileFile(file File) File {
	file = activeProfileFile(file)
	file.BaseURL = ""
	file.ActiveProfile = ""
	return file
}

func saveProfile(file File) File {
	if file.Profiles == nil {
		file.Profiles = make(map[string]Profile)
	}
	name := profileName(file.BaseURL)
	file.ActiveProfile = name
	file.Profiles[name] = Profile{
		BaseURL:             file.BaseURL,
		Deployment:          file.Deployment,
		Email:               file.Email,
		APIToken:            file.APIToken,
		PersonalAccessToken: file.PersonalAccessToken,
		Tools:               append([]string(nil), file.Tools...),
	}
	return file
}

func profileName(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return "default"
}

func detectBoards(out io.Writer, file File) {
	cfg, err := toJiraConfig(file)
	if err != nil {
		_, _ = fmt.Fprintf(out, "\nJira connection check skipped: %v\n", err)
		return
	}
	boards, err := jira.NewClient(cfg).ListBoards("", 200)
	if err != nil {
		_, _ = fmt.Fprintf(out, "\nJira connection check failed: %v\n", err)
		return
	}
	_, _ = fmt.Fprintf(out, "\nBoards detected: %d\n", len(boards))
	for _, board := range boards {
		_, _ = fmt.Fprintf(out, "  - %d %q (%s, project=%s)\n", board.ID, board.Name, board.Type, board.Location.ProjectKey)
	}
}

func configureTools(r *bufio.Reader, out io.Writer, current, choices []string) ([]string, error) {
	if len(current) == 0 {
		current = append([]string(nil), choices...)
	}
	_, _ = fmt.Fprintln(out, "\nTools (selected entries will remain enabled):")
	selected := make(map[string]bool, len(current))
	for _, name := range current {
		selected[name] = true
	}
	for i, name := range choices {
		marker := "[ ]"
		if selected[name] {
			marker = "[x]"
		}
		_, _ = fmt.Fprintf(out, "  %s %d. %s\n", marker, i+1, name)
	}
	selectionValue, err := ask(r, out, "Enabled tools (numbers/names, or 'all')", strings.Join(current, ","), true)
	if err != nil {
		return nil, err
	}
	return selection.Parse(selectionValue, choices)
}

func detectedInstallTargets() []installTarget {
	var targets []installTarget
	add := func(name, path, format string, installed bool) {
		if _, err := os.Stat(path); err != nil && !installed {
			return
		}
		targets = append(targets, installTarget{Name: name, Path: path, Format: format, Configured: targetConfigured(path, format)})
	}
	openCodeInstalled := commandExists("opencode")
	add("OpenCode (global)", filepath.Join(configRoot(), "opencode", "opencode.json"), "opencode", openCodeInstalled)
	add("OpenCode (current project)", filepath.Join(".", "opencode.json"), "opencode", openCodeInstalled)
	claudeInstalled := commandExists("claude")
	add("Claude Code (global)", filepath.Join(homeDir(), ".claude.json"), "claude", claudeInstalled)
	add("Claude Code (current project)", filepath.Join(".", ".mcp.json"), "claude", claudeInstalled)
	add("Claude Desktop", claudeDesktopConfigPath(), "claude", false)
	add("OpenAI Codex CLI", filepath.Join(homeDir(), ".codex", "config.toml"), "codex", commandExists("codex"))
	add("VS Code (current project)", filepath.Join(".vscode", "mcp.json"), "vscode", commandExists("code"))
	add("Zed (global)", filepath.Join(configRoot(), "zed", "settings.json"), "zed", commandExists("zed"))
	return targets
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func claudeDesktopConfigPath() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Claude", "claude_desktop_config.json")
		}
	}
	return filepath.Join(configRoot(), "Claude", "claude_desktop_config.json")
}

func configRoot() string {
	if root := os.Getenv("XDG_CONFIG_HOME"); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config"
	}
	return filepath.Join(home, ".config")
}

func addToOpenCode(path string, file File) error {
	return updateTarget(installTarget{Path: path, Format: "opencode"}, file)
}

func targetConfigured(path, format string) bool {
	if format == "codex" {
		b, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(b), "[mcp_servers.jira]")
	}
	config, err := readJSONConfig(path)
	if err != nil {
		return false
	}
	servers, ok := config[targetServerKey(format)].(map[string]any)
	if !ok {
		return false
	}
	_, ok = servers["jira"]
	return ok
}

func updateTarget(target installTarget, file File) error {
	if target.Format == "codex" {
		return updateCodexConfig(target.Path, file, false)
	}
	config, err := readJSONConfig(target.Path)
	if err != nil {
		return err
	}
	if _, ok := config["$schema"]; !ok && target.Format == "opencode" {
		config["$schema"] = "https://opencode.ai/config.json"
	}
	key := targetServerKey(target.Format)
	servers, ok := config[key].(map[string]any)
	if !ok {
		servers = map[string]any{}
	}
	servers["jira"] = serverEntry(target.Format, file)
	config[key] = servers
	return writeJSONConfig(target.Path, config)
}

func removeFromTarget(target installTarget) error {
	if target.Format == "codex" {
		return updateCodexConfig(target.Path, File{}, true)
	}
	config, err := readJSONConfig(target.Path)
	if err != nil {
		return err
	}
	key := targetServerKey(target.Format)
	servers, ok := config[key].(map[string]any)
	if !ok {
		return nil
	}
	delete(servers, "jira")
	config[key] = servers
	return writeJSONConfig(target.Path, config)
}

func readJSONConfig(path string) (map[string]any, error) {
	config := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &config); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return config, nil
}

func writeJSONConfig(path string, config map[string]any) error {
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

func targetServerKey(format string) string {
	switch format {
	case "opencode":
		return "mcp"
	case "vscode":
		return "servers"
	case "zed":
		return "context_servers"
	default:
		return "mcpServers"
	}
}

func serverEntry(format string, file File) map[string]any {
	environment := map[string]string{
		"JIRA_MCP_PROFILE": file.ActiveProfile,
	}
	command := executablePath()
	switch format {
	case "opencode":
		return map[string]any{"type": "local", "command": []string{command}, "environment": environment, "enabled": true}
	case "vscode":
		return map[string]any{"type": "stdio", "command": command, "args": []string{}, "env": environment}
	case "zed":
		return map[string]any{"command": map[string]any{"path": command, "args": []string{}, "env": environment}}
	default:
		return map[string]any{"command": command, "args": []string{}, "env": environment}
	}
}

func updateCodexConfig(path string, file File, remove bool) error {
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(string(content), "\n")
	filtered := make([]string, 0, len(lines))
	inJiraSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inJiraSection = trimmed == "[mcp_servers.jira]" || trimmed == "[mcp_servers.jira.env]"
		}
		if !inJiraSection {
			filtered = append(filtered, line)
		}
	}
	result := strings.TrimRight(strings.Join(filtered, "\n"), "\n")
	if !remove {
		result += "\n\n[mcp_servers.jira]\n"
		result += "command = " + strconv.Quote(executablePath()) + "\n"
		result += "args = []\n\n[mcp_servers.jira.env]\n"
		for _, name := range []string{"JIRA_MCP_PROFILE"} {
			if value := codexEnvironment(file)[name]; value != "" {
				result += name + " = " + strconv.Quote(value) + "\n"
			}
		}
	}
	if result != "" {
		result += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(result), 0600)
}

func codexEnvironment(file File) map[string]string {
	return map[string]string{"JIRA_MCP_PROFILE": file.ActiveProfile}
}

func executablePath() string {
	path, err := os.Executable()
	if err == nil {
		return path
	}
	return "jira-mcp"
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

func normalizeDeployment(value string) string {
	deployment := strings.ToLower(strings.TrimSpace(value))
	switch deployment {
	case "data center", "data-center", "datacenter", "dc":
		return "server"
	default:
		return deployment
	}
}

func enabledTools(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	result := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			result[name] = true
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
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

func askSecret(r *bufio.Reader, out io.Writer, label, current string, interactive bool) (string, error) {
	if current == "" {
		_, _ = fmt.Fprintf(out, "%s (input hidden): ", label)
	} else {
		_, _ = fmt.Fprintf(out, "%s [saved; Enter keeps it, type a replacement; input hidden]: ", label)
	}
	var value string
	var err error
	if interactive {
		password, readErr := term.ReadPassword(int(os.Stdin.Fd()))
		_, _ = fmt.Fprintln(out)
		value = string(password)
		err = readErr
	} else {
		value, err = r.ReadString('\n')
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = current
	}
	if value == "" {
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
