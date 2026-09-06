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

const version = "1.0.0"

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
		PublicURL:     strings.TrimSpace(os.Getenv("JIRA_MCP_PUBLIC_URL")),
		AtlassianID:   strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_CLIENT_ID")),
		AtlassianKey:  strings.TrimSpace(os.Getenv("JIRA_MCP_ATLASSIAN_CLIENT_SECRET")),
		Store:         store,
		EncryptionKey: key,
	})
	if err != nil {
		return err
	}
	addr := strings.TrimSpace(os.Getenv("JIRA_MCP_LISTEN_ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	return http.ListenAndServe(addr, server)
}

func loadConfig() (jira.Config, error) {
	baseURL := strings.TrimSpace(os.Getenv("JIRA_BASE_URL"))
	if baseURL == "" {
		return jira.Config{}, fmt.Errorf("JIRA_BASE_URL não foi definido")
	}

	deployment := jira.DeploymentCloud
	switch strings.ToLower(strings.TrimSpace(os.Getenv("JIRA_DEPLOYMENT"))) {
	case "", "cloud":
		deployment = jira.DeploymentCloud
	case "server", "datacenter", "data-center", "dc":
		deployment = jira.DeploymentServer
	default:
		return jira.Config{}, fmt.Errorf("JIRA_DEPLOYMENT inválido: use 'cloud' ou 'server'")
	}

	cfg := jira.Config{
		BaseURL:    baseURL,
		Deployment: deployment,
	}

	switch deployment {
	case jira.DeploymentCloud:
		cfg.Email = strings.TrimSpace(os.Getenv("JIRA_EMAIL"))
		cfg.APIToken = strings.TrimSpace(os.Getenv("JIRA_API_TOKEN"))
		if cfg.Email == "" || cfg.APIToken == "" {
			return jira.Config{}, fmt.Errorf("JIRA_EMAIL e JIRA_API_TOKEN são obrigatórios para JIRA_DEPLOYMENT=cloud")
		}
	case jira.DeploymentServer:
		cfg.PersonalAccessToken = strings.TrimSpace(os.Getenv("JIRA_PERSONAL_ACCESS_TOKEN"))
		if cfg.PersonalAccessToken == "" {
			return jira.Config{}, fmt.Errorf("JIRA_PERSONAL_ACCESS_TOKEN é obrigatório para JIRA_DEPLOYMENT=server")
		}
	}

	return cfg, nil
}
