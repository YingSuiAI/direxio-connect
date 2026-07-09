package reasonix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/YingSuiAI/dirextalk-connect/core"
	"github.com/stretchr/testify/require"
)

func TestEnsureReasonixMCPConfigWritesProjectConfig(t *testing.T) {
	workDir := t.TempDir()
	cfg := core.MCPConfigFromAgentToken("Dirextalk.D1", "https://d1.dirextalk.ai/mcp", "agent-token", "node-1")

	require.NoError(t, ensureReasonixMCPConfig(workDir, cfg))

	path := filepath.Join(workDir, ".mcp.json")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	doc := readReasonixMCPConfig(t, path)
	servers := doc["mcpServers"].(map[string]any)
	server := servers["dirextalk_d1"].(map[string]any)
	require.Equal(t, "http", server["type"])
	require.Equal(t, "https://d1.dirextalk.ai/mcp", server["url"])

	headers := server["headers"].(map[string]any)
	require.Equal(t, "Bearer agent-token", headers["Authorization"])
	require.Equal(t, "node-1", headers["DIREXTALK-Agent-Node-Id"])
}

func TestEnsureReasonixMCPConfigPreservesExistingServers(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, ".mcp.json")
	existing := []byte(`{
  "mcpServers": {
    "existing": {
      "type": "stdio",
      "command": "example"
    }
  },
  "other": true
}`)
	require.NoError(t, os.WriteFile(path, existing, 0o600))

	cfg := core.MCPConfigFromAgentToken("Dirextalk.D1", "https://d1.dirextalk.ai/mcp", "agent-token", "")
	require.NoError(t, ensureReasonixMCPConfig(workDir, cfg))

	doc := readReasonixMCPConfig(t, path)
	require.Equal(t, true, doc["other"])

	servers := doc["mcpServers"].(map[string]any)
	existingServer := servers["existing"].(map[string]any)
	require.Equal(t, "stdio", existingServer["type"])
	require.Equal(t, "example", existingServer["command"])

	server := servers["dirextalk_d1"].(map[string]any)
	headers := server["headers"].(map[string]any)
	require.Equal(t, "Bearer agent-token", headers["Authorization"])
	require.NotContains(t, headers, "DIREXTALK-Agent-Node-Id")
}

func readReasonixMCPConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	doc := map[string]any{}
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}
