package core

import (
	"fmt"
	"strings"
)

// PlatformFactory creates a Platform from config options.
type PlatformFactory func(opts map[string]any) (Platform, error)

// AgentFactory creates an Agent from config options.
type AgentFactory func(opts map[string]any) (Agent, error)

type MCPCapabilityKind string

const (
	MCPCapabilitySession     MCPCapabilityKind = "session"
	MCPCapabilityProject     MCPCapabilityKind = "project"
	MCPCapabilityHostManaged MCPCapabilityKind = "host-managed"
	MCPCapabilityConditional MCPCapabilityKind = "conditional"
	MCPCapabilityUnsupported MCPCapabilityKind = "unsupported"
)

// MCPBackendCapability declares how an agent backend can consume the canonical
// remote HTTP MCP description.
type MCPBackendCapability struct {
	Kind              MCPCapabilityKind
	ConditionalOption string
	Reason            string
}

type agentRegistration struct {
	factory       AgentFactory
	mcpCapability MCPBackendCapability
}

var (
	platformFactories  = make(map[string]PlatformFactory)
	agentRegistrations = make(map[string]agentRegistration)
)

func RegisterPlatform(name string, factory PlatformFactory) {
	platformFactories[name] = factory
}

func RegisterAgent(name string, factory AgentFactory, capability MCPBackendCapability) {
	switch capability.Kind {
	case MCPCapabilitySession, MCPCapabilityProject, MCPCapabilityHostManaged, MCPCapabilityConditional, MCPCapabilityUnsupported:
		// Explicit capability declarations are required for every backend.
	default:
		panic(fmt.Sprintf("agent %q has invalid MCP capability %q", name, capability.Kind))
	}
	if capability.Kind == MCPCapabilityConditional && strings.TrimSpace(capability.ConditionalOption) == "" {
		panic(fmt.Sprintf("agent %q has conditional MCP capability without a condition option", name))
	}
	agentRegistrations[name] = agentRegistration{factory: factory, mcpCapability: capability}
}

func CreatePlatform(name string, opts map[string]any) (Platform, error) {
	f, ok := platformFactories[name]
	if !ok {
		available := make([]string, 0, len(platformFactories))
		for k := range platformFactories {
			available = append(available, k)
		}
		return nil, fmt.Errorf("unknown platform %q, available: %v", name, available)
	}
	return f(opts)
}

func ListRegisteredAgents() []string {
	names := make([]string, 0, len(agentRegistrations))
	for k := range agentRegistrations {
		names = append(names, k)
	}
	return names
}

func ListRegisteredPlatforms() []string {
	names := make([]string, 0, len(platformFactories))
	for k := range platformFactories {
		names = append(names, k)
	}
	return names
}

func CreateAgent(name string, opts map[string]any) (Agent, error) {
	registration, ok := agentRegistrations[name]
	if !ok {
		available := make([]string, 0, len(agentRegistrations))
		for k := range agentRegistrations {
			available = append(available, k)
		}
		return nil, fmt.Errorf("unknown agent %q, available: %v", name, available)
	}
	if err := ValidateMCPOptions(opts); err != nil {
		return nil, fmt.Errorf("agent %q: %w", name, err)
	}
	if ParseMCPConfig(opts).Enabled() {
		capability, err := effectiveMCPCapability(registration.mcpCapability, opts)
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", name, err)
		}
		switch capability.Kind {
		case MCPCapabilitySession, MCPCapabilityProject:
			// The backend owns its official schema and injection path.
		case MCPCapabilityConditional:
			if !conditionalMCPConsumerDeclared(opts, capability.ConditionalOption) {
				return nil, fmt.Errorf("MCP capability is conditional; set %s to declare the required MCP consumer: %s", capability.ConditionalOption, capability.Reason)
			}
			// Conditional wrappers consume the canonical values from their child
			// process environment. Native session/project adapters do not.
			opts = WithMCPEnvOptions(opts)
		case MCPCapabilityHostManaged, MCPCapabilityUnsupported:
			return nil, fmt.Errorf("MCP capability is %s: %s", capability.Kind, capability.Reason)
		default:
			return nil, fmt.Errorf("MCP capability is unsupported: backend has no explicit capability entry")
		}
	}
	return registration.factory(opts)
}

func effectiveMCPCapability(capability MCPBackendCapability, opts map[string]any) (MCPBackendCapability, error) {
	override, _ := opts["mcp_capability"].(string)
	override = strings.TrimSpace(strings.ToLower(override))
	if override == "" || MCPCapabilityKind(override) == capability.Kind {
		return capability, nil
	}
	// Runtime adapters such as OpenClaw may be more restrictive than their
	// generic ACP transport. Never allow config to upgrade a registry entry.
	switch MCPCapabilityKind(override) {
	case MCPCapabilityHostManaged, MCPCapabilityUnsupported:
		return MCPBackendCapability{
			Kind:   MCPCapabilityKind(override),
			Reason: "selected runtime requires external MCP configuration",
		}, nil
	default:
		return MCPBackendCapability{}, fmt.Errorf("invalid mcp_capability override %q; only host-managed or unsupported restrictions are allowed", override)
	}
}

func conditionalMCPConsumerDeclared(opts map[string]any, key string) bool {
	if key == "" {
		return false
	}
	switch value := opts[key].(type) {
	case bool:
		return value
	case string:
		value = strings.TrimSpace(value)
		return value != "" && !strings.EqualFold(value, "false") && !strings.EqualFold(value, "off")
	default:
		return false
	}
}

func GetAgentMCPCapability(name string) (MCPBackendCapability, bool) {
	registration, ok := agentRegistrations[name]
	if !ok {
		return MCPBackendCapability{}, false
	}
	return registration.mcpCapability, true
}
