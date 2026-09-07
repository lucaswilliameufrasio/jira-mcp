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
	"strings"

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
	server, err := remote.New(remote.Config{
		PublicURL:        strings.TrimSpace(os.Getenv("JIRA_MCP_PUBLIC_URL")),
		AtlassianID:      strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_CLIENT_ID")),
		AtlassianKey:     strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_CLIENT_SECRET")),
		AtlassianAuthURL: strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_AUTH_URL")),
		AtlassianAPIURL:  strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_API_URL")),
		Store:            store,
		EncryptionKey:    key,
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
	return http.ListenAndServe(addr, server)
}
