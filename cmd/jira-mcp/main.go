// Command jira-mcp is a Model Context Protocol server that exposes Atlassian
// Jira operations (search, read, create, update, transition, comment,
// assign, list projects) as MCP tools over stdio.
//
// Configuration is via environment variables:
//
//	JIRA_BASE_URL      Site root, e.g. https://yourcompany.atlassian.net (required)
//	JIRA_DEPLOYMENT    "cloud" (default) or "server"
//
//	# Cloud auth (JIRA_DEPLOYMENT=cloud):
//	JIRA_EMAIL         Account email address
//	JIRA_API_TOKEN     API token, from https://id.atlassian.com/manage-profile/security/api-tokens
//
//	# Server/Data Center auth (JIRA_DEPLOYMENT=server):
//	JIRA_PERSONAL_ACCESS_TOKEN   Personal Access Token
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"jira-mcp/internal/config"
	"jira-mcp/internal/jira"
	"jira-mcp/internal/mcp"
	"jira-mcp/internal/remote"
	"jira-mcp/internal/tools"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := config.RunSetup(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "[jira-mcp] setup error:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		if err := runDoctor(); err != nil {
			fmt.Fprintln(os.Stderr, "[jira-mcp] doctor found problems:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "remote" {
		if err := runRemote(); err != nil {
			fmt.Fprintln(os.Stderr, "[jira-mcp] remote error:", err)
			os.Exit(1)
		}
		return
	}

	cfg, enabled, err := config.Resolve()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[jira-mcp] configuration error:", err)
		os.Exit(1)
	}

	client := jira.NewClient(cfg)
	server := mcp.NewServerWithTools("jira-mcp", version, enabled)
	tools.Register(server, client)

	server.Logf("starting jira-mcp %s (deployment=%s, base_url=%s)", version, cfg.Deployment, cfg.BaseURL)

	if err := server.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "[jira-mcp] fatal error:", err)
		os.Exit(1)
	}
}

func runDoctor() error {
	configErr := config.Doctor(os.Stdout)
	_, _ = fmt.Fprintln(os.Stdout, "\nMCP stdio")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := executablePath()
	cmd := exec.Command(command)
	cmd.Stderr = os.Stderr
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "jira-mcp-doctor", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stdout, "  FAIL  %v\n", err)
		return joinErrors(configErr, err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stdout, "  FAIL  tools/list: %v\n", err)
		return joinErrors(configErr, err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "  OK    stdio handshake; %d tools exposed\n", len(listed.Tools))
	return configErr
}

func executablePath() string {
	path, err := os.Executable()
	if err == nil {
		return path
	}
	return "jira-mcp"
}

func joinErrors(first, second error) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return fmt.Errorf("%v; %w", first, second)
}

func runRemote() error {
	store, err := remote.NewValkeyStore(strings.TrimSpace(os.Getenv("VALKEY_URL")))
	if err != nil {
		return err
	}
	defer store.Close()
	key, err := remote.EncryptionKeyFromEnv()
	if err != nil {
		return err
	}
	rateLimit := float64(remote.DefaultRateLimitRPS)
	if raw := strings.TrimSpace(os.Getenv("JIRA_MCP_RATE_LIMIT_RPS")); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("invalid JIRA_MCP_RATE_LIMIT_RPS %q", raw)
		}
		rateLimit = max(0, parsed)
	}
	server, err := remote.New(remote.Config{
		PublicURL:        strings.TrimSpace(os.Getenv("JIRA_MCP_PUBLIC_URL")),
		AtlassianID:      strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_CLIENT_ID")),
		AtlassianKey:     strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_CLIENT_SECRET")),
		AtlassianAuthURL: strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_AUTH_URL")),
		AtlassianAPIURL:  strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_API_URL")),
		Store:            store,
		EncryptionKey:    key,
		RateLimitRPS:     rateLimit,
	})
	if err != nil {
		return err
	}
	addr := strings.TrimSpace(os.Getenv("JIRA_MCP_LISTEN_ADDR"))
	if addr == "" {
		host := strings.TrimSpace(os.Getenv("HOST"))
		if host == "" {
			host = "0.0.0.0"
		}
		port := strings.TrimSpace(os.Getenv("PORT"))
		if port == "" {
			port = "8080"
		}
		addr = host + ":" + port
	}
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	return httpServer.ListenAndServe()
}
