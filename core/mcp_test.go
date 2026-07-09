package core

import (
	"context"
	"testing"
)

func TestParseMCPConfig_FromAgentToken(t *testing.T) {
	cfg := ParseMCPConfig(map[string]any{
		"mcp_server_name": "Dirextalk.D1",
		"mcp_url":         "https://d1.dirextalk.ai/mcp",
		"mcp_agent_token": "agent-token",
		"mcp_node_id":     "codex-d1",
	})

	if !cfg.Enabled() {
		t.Fatal("MCP config should be enabled")
	}
	if cfg.ServerName != "dirextalk_d1" {
		t.Fatalf("ServerName = %q", cfg.ServerName)
	}
	if cfg.Authorization != "Bearer agent-token" {
		t.Fatalf("Authorization = %q", cfg.Authorization)
	}
	if cfg.NodeID != "codex-d1" {
		t.Fatalf("NodeID = %q", cfg.NodeID)
	}
}

func TestWithMCPEnvOptions_MergesEnv(t *testing.T) {
	opts := WithMCPEnvOptions(map[string]any{
		"mcp_server_name": "dirextalk-d1",
		"mcp_domain":      "d1.dirextalk.ai",
		"mcp_agent_token": "agent-token",
		"mcp_node_id":     "node-1",
		"env": map[string]any{
			"EXISTING": "keep",
		},
	})

	env, ok := opts["env"].(map[string]any)
	if !ok {
		t.Fatalf("env type = %T", opts["env"])
	}
	if env["EXISTING"] != "keep" {
		t.Fatalf("EXISTING env lost: %#v", env)
	}
	if env["DIREXTALK_MCP_URL"] != "https://d1.dirextalk.ai/mcp" {
		t.Fatalf("DIREXTALK_MCP_URL = %#v", env["DIREXTALK_MCP_URL"])
	}
	if env["DIREXTALK_MCP_AGENT_TOKEN"] != "agent-token" {
		t.Fatalf("DIREXTALK_MCP_AGENT_TOKEN missing")
	}
	if env["DIREXTALK_MCP_NODE_ID"] != "node-1" {
		t.Fatalf("DIREXTALK_MCP_NODE_ID = %#v", env["DIREXTALK_MCP_NODE_ID"])
	}
}

func TestMCPConfig_ACPServers(t *testing.T) {
	cfg := MCPConfigFromAgentToken("dirextalk-d1", "https://d1.dirextalk.ai/mcp", "agent-token", "node-1")
	servers := cfg.ACPServers()
	if len(servers) != 1 {
		t.Fatalf("servers len = %d", len(servers))
	}
	server, ok := servers[0].(map[string]any)
	if !ok {
		t.Fatalf("server type = %T", servers[0])
	}
	if server["name"] != "dirextalk-d1" || server["url"] != "https://d1.dirextalk.ai/mcp" {
		t.Fatalf("server = %#v", server)
	}
	if server["type"] != "http" {
		t.Fatalf("type = %#v, want http", server["type"])
	}
	headers, ok := server["headers"].([]map[string]string)
	if !ok {
		t.Fatalf("headers type = %T", server["headers"])
	}
	gotHeaders := map[string]string{}
	for _, header := range headers {
		gotHeaders[header["name"]] = header["value"]
	}
	if gotHeaders["Authorization"] != "Bearer agent-token" {
		t.Fatalf("Authorization header = %q", gotHeaders["Authorization"])
	}
	if gotHeaders["DIREXTALK-Agent-Node-Id"] != "node-1" {
		t.Fatalf("node header = %q", gotHeaders["DIREXTALK-Agent-Node-Id"])
	}
}

func TestCreateAgent_AddsMCPEnvForAllFactories(t *testing.T) {
	agentName := "mcp-test-agent"
	var captured map[string]any
	RegisterAgent(agentName, func(opts map[string]any) (Agent, error) {
		captured = opts
		return stubMCPAgent{}, nil
	})

	_, err := CreateAgent(agentName, map[string]any{
		"mcp_server_name": "dirextalk-d1",
		"mcp_url":         "https://d1.dirextalk.ai/mcp",
		"mcp_agent_token": "agent-token",
	})
	if err != nil {
		t.Fatalf("CreateAgent() error = %v", err)
	}
	env, ok := captured["env"].(map[string]any)
	if !ok {
		t.Fatalf("env type = %T", captured["env"])
	}
	if env["DIREXTALK_MCP_SERVER_NAME"] != "dirextalk-d1" {
		t.Fatalf("MCP env not injected: %#v", env)
	}
}

type stubMCPAgent struct{}

func (stubMCPAgent) Name() string { return "mcp-test-agent" }
func (stubMCPAgent) StartSession(context.Context, string) (AgentSession, error) {
	return nil, nil
}
func (stubMCPAgent) ListSessions(context.Context) ([]AgentSessionInfo, error) {
	return nil, nil
}
func (stubMCPAgent) Stop() error { return nil }
