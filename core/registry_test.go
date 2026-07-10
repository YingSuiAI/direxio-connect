package core

import (
	"context"
	"strings"
	"testing"
)

type stubPlatform struct{ n string }

func (s *stubPlatform) Name() string                                   { return s.n }
func (s *stubPlatform) Start(MessageHandler) error                     { return nil }
func (s *stubPlatform) Reply(_ context.Context, _ any, _ string) error { return nil }
func (s *stubPlatform) Send(_ context.Context, _ any, _ string) error  { return nil }
func (s *stubPlatform) Stop() error                                    { return nil }

func TestRegisterAndCreatePlatform(t *testing.T) {
	RegisterPlatform("test-plat", func(opts map[string]any) (Platform, error) {
		return &stubPlatform{n: "test-plat"}, nil
	})

	p, err := CreatePlatform("test-plat", nil)
	if err != nil {
		t.Fatalf("CreatePlatform: %v", err)
	}
	if p.Name() != "test-plat" {
		t.Errorf("Name() = %q, want test-plat", p.Name())
	}
}

func TestCreatePlatform_Unknown(t *testing.T) {
	_, err := CreatePlatform("nonexistent-xyz", nil)
	if err == nil {
		t.Error("expected error for unknown platform")
	}
}

func TestCreateAgent_Unknown(t *testing.T) {
	_, err := CreateAgent("nonexistent-xyz", nil)
	if err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestRegisterAgentRejectsMissingExplicitMCPCapability(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterAgent accepted an empty MCP capability")
		}
	}()
	RegisterAgent("missing-capability", func(map[string]any) (Agent, error) {
		return stubMCPAgent{}, nil
	}, MCPBackendCapability{})
}

func TestCreateAgent_ValidatesPartialMCPBeforeCallingFactory(t *testing.T) {
	name := "partial-mcp-agent"
	called := false
	RegisterAgent(name, func(map[string]any) (Agent, error) {
		called = true
		return stubMCPAgent{}, nil
	}, MCPBackendCapability{Kind: MCPCapabilitySession})

	_, err := CreateAgent(name, map[string]any{"mcp_url": "https://d1.dirextalk.ai/mcp"})
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("CreateAgent() error = %v, want partial MCP error", err)
	}
	if called {
		t.Fatal("factory called for invalid MCP configuration")
	}
}

func TestCreateAgent_FailsClosedForUnsupportedAndHostManagedMCP(t *testing.T) {
	for _, capability := range []MCPBackendCapability{
		{Kind: MCPCapabilityUnsupported, Reason: "runtime has no MCP client"},
		{Kind: MCPCapabilityHostManaged, Reason: "configure the host runtime"},
	} {
		name := "closed-" + string(capability.Kind)
		called := false
		RegisterAgent(name, func(map[string]any) (Agent, error) {
			called = true
			return stubMCPAgent{}, nil
		}, capability)

		_, err := CreateAgent(name, completeMCPOptions())
		if err == nil || !strings.Contains(err.Error(), string(capability.Kind)) {
			t.Fatalf("CreateAgent(%s) error = %v, want actionable capability error", capability.Kind, err)
		}
		if called {
			t.Fatalf("factory called for %s MCP backend", capability.Kind)
		}
	}
}

func TestCreateAgent_ConditionalMCPRequiresDeclaredConsumer(t *testing.T) {
	name := "conditional-mcp-agent"
	var captured map[string]any
	RegisterAgent(name, func(opts map[string]any) (Agent, error) {
		captured = opts
		return stubMCPAgent{}, nil
	}, MCPBackendCapability{
		Kind:              MCPCapabilityConditional,
		ConditionalOption: "mcp_wrapper",
		Reason:            "declare an MCP-consuming wrapper",
	})

	_, err := CreateAgent(name, completeMCPOptions())
	if err == nil || !strings.Contains(err.Error(), "mcp_wrapper") {
		t.Fatalf("CreateAgent() error = %v, want conditional option guidance", err)
	}

	opts := completeMCPOptions()
	opts["mcp_wrapper"] = true
	if _, err := CreateAgent(name, opts); err != nil {
		t.Fatalf("CreateAgent() with declared wrapper: %v", err)
	}
	env, _ := captured["env"].(map[string]any)
	if env["DIREXTALK_MCP_URL"] != "https://d1.dirextalk.ai/mcp" {
		t.Fatalf("conditional wrapper did not receive MCP process env: %#v", env)
	}
}

func TestCreateAgent_SessionMCPDoesNotInjectGenericEnvironment(t *testing.T) {
	name := "session-mcp-agent"
	var captured map[string]any
	RegisterAgent(name, func(opts map[string]any) (Agent, error) {
		captured = opts
		return stubMCPAgent{}, nil
	}, MCPBackendCapability{Kind: MCPCapabilitySession})

	if _, err := CreateAgent(name, completeMCPOptions()); err != nil {
		t.Fatalf("CreateAgent(): %v", err)
	}
	if env, ok := captured["env"].(map[string]any); ok {
		if _, leaked := env["DIREXTALK_MCP_AUTHORIZATION"]; leaked {
			t.Fatalf("session backend received generic MCP env: %#v", env)
		}
	}
}

func completeMCPOptions() map[string]any {
	return map[string]any{
		"mcp_server_name": "dirextalk-d1",
		"mcp_url":         "https://d1.dirextalk.ai/mcp",
		"mcp_agent_token": "agent-token",
		"mcp_node_id":     "node-1",
	}
}
