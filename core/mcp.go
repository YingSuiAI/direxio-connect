package core

import (
	"fmt"
	"net/url"
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

// ValidateMCPOptions validates whether the MCP-related options form a complete
// canonical remote MCP description. An explicitly disabled configuration is
// ignored so operators can keep staged values without activating MCP.
func ValidateMCPOptions(opts map[string]any) error {
	if !mcpOptionsRequested(opts) || !optionBool(opts, "mcp_enabled", true) {
		return nil
	}

	missing := make([]string, 0, 3)
	if SanitizeMCPServerName(stringOption(opts, "mcp_server_name")) == "" {
		missing = append(missing, "mcp_server_name")
	}
	if stringOption(opts, "mcp_url") == "" && stringOption(opts, "mcp_domain") == "" {
		missing = append(missing, "mcp_url (or mcp_domain)")
	}
	if stringOption(opts, "mcp_authorization") == "" && stringOption(opts, "mcp_agent_token") == "" {
		missing = append(missing, "mcp_agent_token (or mcp_authorization)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("MCP configuration is incomplete; missing %s", strings.Join(missing, ", "))
	}

	cfg := ParseMCPConfig(opts)
	if err := validateMCPURL(cfg.URL); err != nil {
		return fmt.Errorf("MCP configuration has invalid mcp_url: %w", err)
	}
	if err := validateMCPAuthorization(cfg.Authorization); err != nil {
		return fmt.Errorf("MCP configuration has invalid mcp_authorization: %w", err)
	}
	return nil
}

func validateMCPURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !parsed.IsAbs() || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		return fmt.Errorf("must be an absolute HTTPS URL with a non-empty host")
	}
	if parsed.Path != "/mcp" || parsed.RawPath != "" && parsed.EscapedPath() != "/mcp" {
		return fmt.Errorf("path must be exactly /mcp")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return fmt.Errorf("query strings and fragments are not allowed")
	}
	return nil
}

func validateMCPAuthorization(raw string) error {
	authorization := strings.TrimSpace(raw)
	scheme, token, found := strings.Cut(authorization, " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t\r\n") {
		return fmt.Errorf("must be Bearer followed by one non-empty token")
	}
	return nil
}

func mcpOptionsRequested(opts map[string]any) bool {
	if opts == nil {
		return false
	}
	for _, key := range []string{
		"mcp_enabled", "mcp_server_name", "mcp_url", "mcp_domain",
		"mcp_authorization", "mcp_agent_token", "mcp_node_id",
	} {
		if _, ok := opts[key]; ok {
			return true
		}
	}
	switch nested := opts["mcp"].(type) {
	case map[string]any:
		return len(nested) > 0
	case map[string]string:
		return len(nested) > 0
	default:
		return false
	}
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
