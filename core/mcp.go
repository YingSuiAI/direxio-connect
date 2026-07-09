package core

import (
	"fmt"
	"strings"
)

// MCPConfig is the shared Dirextalk remote-MCP description passed to agents.
// Agent backends may consume it natively, or use Env to expose it to child CLIs.
type MCPConfig struct {
	ServerName    string
	URL           string
	Authorization string
	NodeID        string
}

func ParseMCPConfig(opts map[string]any) MCPConfig {
	if !optionBool(opts, "mcp_enabled", true) {
		return MCPConfig{}
	}
	serverName := stringOption(opts, "mcp_server_name")
	url := stringOption(opts, "mcp_url")
	if url == "" {
		url = mcpURLFromDomain(stringOption(opts, "mcp_domain"))
	}
	auth := strings.TrimSpace(stringOption(opts, "mcp_authorization"))
	if auth == "" {
		token := strings.TrimSpace(stringOption(opts, "mcp_agent_token"))
		if token != "" {
			if strings.HasPrefix(strings.ToLower(token), "bearer ") {
				auth = token
			} else {
				auth = "Bearer " + token
			}
		}
	}
	return MCPConfig{
		ServerName:    SanitizeMCPServerName(serverName),
		URL:           strings.TrimSpace(url),
		Authorization: auth,
		NodeID:        strings.TrimSpace(stringOption(opts, "mcp_node_id")),
	}
}

func (c MCPConfig) Enabled() bool {
	return c.ServerName != "" && c.URL != "" && c.Authorization != ""
}

func (c MCPConfig) Env() []string {
	if !c.Enabled() {
		return nil
	}
	env := []string{
		"DIREXTALK_MCP_ENABLED=1",
		"DIREXTALK_MCP_SERVER_NAME=" + c.ServerName,
		"DIREXTALK_MCP_URL=" + c.URL,
		"DIREXTALK_MCP_AUTHORIZATION=" + c.Authorization,
	}
	if strings.HasPrefix(strings.ToLower(c.Authorization), "bearer ") {
		env = append(env, "DIREXTALK_MCP_AGENT_TOKEN="+strings.TrimSpace(c.Authorization[len("bearer "):]))
	}
	if c.NodeID != "" {
		env = append(env, "DIREXTALK_MCP_NODE_ID="+c.NodeID)
	}
	return env
}

func (c MCPConfig) ACPServers() []any {
	if !c.Enabled() {
		return []any{}
	}
	headers := []map[string]string{
		{"name": "Authorization", "value": c.Authorization},
	}
	if c.NodeID != "" {
		headers = append(headers, map[string]string{"name": "DIREXTALK-Agent-Node-Id", "value": c.NodeID})
	}
	return []any{map[string]any{
		"name":    c.ServerName,
		"type":    "http",
		"url":     c.URL,
		"headers": headers,
	}}
}

func (c MCPConfig) MCPServersConfig() map[string]any {
	if !c.Enabled() {
		return map[string]any{"mcpServers": map[string]any{}}
	}
	headers := map[string]string{
		"Authorization": c.Authorization,
	}
	if c.NodeID != "" {
		headers["DIREXTALK-Agent-Node-Id"] = c.NodeID
	}
	return map[string]any{
		"mcpServers": map[string]any{
			c.ServerName: map[string]any{
				"type":    "http",
				"url":     c.URL,
				"headers": headers,
			},
		},
	}
}

func WithMCPEnvOptions(opts map[string]any) map[string]any {
	cfg := ParseMCPConfig(opts)
	if !cfg.Enabled() {
		return opts
	}
	next := make(map[string]any, len(opts)+1)
	for k, v := range opts {
		next[k] = v
	}
	env := map[string]any{}
	switch raw := opts["env"].(type) {
	case map[string]string:
		for k, v := range raw {
			env[k] = v
		}
	case map[string]any:
		for k, v := range raw {
			env[k] = v
		}
	}
	for _, pair := range cfg.Env() {
		k, v, ok := strings.Cut(pair, "=")
		if ok {
			env[k] = v
		}
	}
	next["env"] = env
	return next
}

func stringOption(opts map[string]any, key string) string {
	if opts == nil {
		return ""
	}
	if v, ok := opts[key].(string); ok {
		return strings.TrimSpace(v)
	}
	if nested, ok := opts["mcp"].(map[string]any); ok {
		if v, ok := nested[strings.TrimPrefix(key, "mcp_")].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	if nested, ok := opts["mcp"].(map[string]string); ok {
		if v, ok := nested[strings.TrimPrefix(key, "mcp_")]; ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func optionBool(opts map[string]any, key string, fallback bool) bool {
	raw, ok := opts[key]
	if !ok {
		return fallback
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "0", "false", "off", "no", "disabled", "skip":
			return false
		case "1", "true", "on", "yes", "enabled", "auto", "":
			return true
		}
	}
	return fallback
}

func mcpURLFromDomain(domain string) string {
	domain = strings.TrimRight(strings.TrimSpace(domain), "/")
	if domain == "" {
		return ""
	}
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}
	return domain + "/mcp"
}

func SanitizeMCPServerName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_' || r == '-':
			return r
		case r == '.':
			return '_'
		default:
			return '-'
		}
	}, name)
	return strings.Trim(name, "-_")
}

func MCPConfigFromAgentToken(serverName, url, agentToken, nodeID string) MCPConfig {
	opts := map[string]any{
		"mcp_server_name": serverName,
		"mcp_url":         url,
		"mcp_agent_token": agentToken,
		"mcp_node_id":     nodeID,
	}
	return ParseMCPConfig(opts)
}

func (c MCPConfig) String() string {
	if !c.Enabled() {
		return "disabled"
	}
	return fmt.Sprintf("%s %s", c.ServerName, c.URL)
}
