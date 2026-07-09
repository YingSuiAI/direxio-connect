// Package reasonix bridges cc-connect to a reasonix serve instance.
// It implements the core.Agent interface by forwarding prompts to reasonix's
// HTTP API (POST /submit) and consuming the SSE event stream (GET /events).
//
// Required agent option: serve_url (e.g. "http://localhost:8080").
package reasonix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/YingSuiAI/dirextalk-connect/core"
)

func init() {
	core.RegisterAgent("reasonix", New)
}

// Agent drives a remote reasonix serve instance.
type Agent struct {
	mu        sync.RWMutex
	serveURL  string // e.g. "http://localhost:8080"
	workDir   string // local project directory (for /dir and display)
	mode      string // permission mode: "default", "yolo", "plan"
	mcpConfig core.MCPConfig
}

// New creates a Reasonix agent.
// Required opts key: "serve_url".
func New(opts map[string]any) (core.Agent, error) {
	serveURL, _ := opts["serve_url"].(string)
	serveURL = strings.TrimRight(serveURL, "/")
	// Strip trailing slashes so path-join never produces "//submit".
	if serveURL == "" {
		return nil, fmt.Errorf("reasonix: serve_url is required")
	}
	if _, err := url.Parse(serveURL); err != nil {
		return nil, fmt.Errorf("reasonix: invalid serve_url %q: %w", serveURL, err)
	}

	workDir, _ := opts["work_dir"].(string)
	if workDir == "" {
		workDir = "."
	}

	mode := normalizeMode(opts)

	slog.Info("reasonix: agent created", "serve_url", serveURL, "work_dir", workDir, "mode", mode)
	return &Agent{
		serveURL:  serveURL,
		workDir:   workDir,
		mode:      mode,
		mcpConfig: core.ParseMCPConfig(opts),
	}, nil
}

func normalizeMode(opts map[string]any) string {
	raw, _ := opts["mode"].(string)
	// "auto" and "force" are legacy aliases from reasonix serve's CLI flags;
	// both map to "yolo" (no interactive approval). All unrecognised values
	// fall back to "default" (interactive approval per tool).
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yolo", "auto", "force":
		return "yolo"
	case "plan":
		return "plan"
	default:
		return "default"
	}
}

func (a *Agent) Name() string { return "reasonix" }

// StartSession creates a session connected to reasonix serve.
// It establishes an SSE connection to /events and waits for it to be ready.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	slog.Info("reasonix: starting session", "session_id", sessionID)
	a.mu.RLock()
	serveURL := a.serveURL
	workDir := a.workDir
	mode := a.mode
	mcpConfig := a.mcpConfig
	a.mu.RUnlock()

	if err := ensureReasonixMCPConfig(workDir, mcpConfig); err != nil {
		return nil, fmt.Errorf("reasonix: configure MCP: %w", err)
	}

	s, err := newSession(ctx, serveURL, workDir, sessionID, mode)
	if err != nil {
		return nil, fmt.Errorf("reasonix: start session: %w", err)
	}
	return s, nil
}

// ListSessions returns nil because reasonix doesn't expose session listing.
func (a *Agent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}

// Stop shuts down the agent.
func (a *Agent) Stop() error { return nil }

// ── WorkDirSwitcher ──────────────────────────────────────────────

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workDir = dir
	slog.Info("reasonix: work_dir changed", "work_dir", dir)
}

func (a *Agent) GetWorkDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workDir
}

// ── ModeSwitcher ─────────────────────────────────────────────────

func (a *Agent) SetMode(mode string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = normalizeMode(map[string]any{"mode": mode})
	slog.Info("reasonix: mode changed", "mode", a.mode)
}

func (a *Agent) GetMode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{Key: "default", Name: "Default", NameZh: "默认",
			Desc: "Prompt for approval on each tool use", DescZh: "每次工具调用都需要确认"},
		{Key: "yolo", Name: "YOLO", NameZh: "全自动",
			Desc: "Auto-approve all tool calls", DescZh: "自动批准所有工具调用"},
		{Key: "plan", Name: "Plan", NameZh: "规划模式",
			Desc: "Read-only plan mode, no execution", DescZh: "只读规划模式，不做修改"},
	}
}

// ── ContextCompressor ──────────────────────────────────────────

// CompressCommand returns "/compact" which gets sent as a prompt to reasonix.
// The session's Send() method will translate special commands.
func (a *Agent) CompressCommand() string { return "/compact" }

// ── MemoryFileProvider ─────────────────────────────────────────

func (a *Agent) ProjectMemoryFile() string {
	absDir, err := filepath.Abs(a.workDir)
	if err != nil {
		absDir = a.workDir
	}
	return filepath.Join(absDir, "REASONIX.md")
}

func (a *Agent) GlobalMemoryFile() string { return "" }

func ensureReasonixMCPConfig(workDir string, cfg core.MCPConfig) error {
	if !cfg.Enabled() {
		return nil
	}
	path, err := reasonixProjectMCPConfigPath(workDir)
	if err != nil {
		return err
	}
	return writeReasonixMCPConfig(path, cfg)
}

func reasonixProjectMCPConfigPath(workDir string) (string, error) {
	if workDir == "" {
		workDir = "."
	}
	absDir, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolve work_dir: %w", err)
	}
	return filepath.Join(absDir, ".mcp.json"), nil
}

func writeReasonixMCPConfig(path string, cfg core.MCPConfig) error {
	doc := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("read Reasonix MCP config %s: %w", path, err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read Reasonix MCP config %s: %w", path, err)
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	headers := map[string]string{
		"Authorization": cfg.Authorization,
	}
	if cfg.NodeID != "" {
		headers["DIREXTALK-Agent-Node-Id"] = cfg.NodeID
	}
	servers[cfg.ServerName] = map[string]any{
		"type":    "http",
		"url":     cfg.URL,
		"headers": headers,
	}
	doc["mcpServers"] = servers

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Reasonix MCP config dir %s: %w", filepath.Dir(path), err)
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encode Reasonix MCP config %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-*.json")
	if err != nil {
		return fmt.Errorf("create temp Reasonix MCP config %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp Reasonix MCP config %s: %w", tmpPath, err)
	}
	if _, err := tmp.Write(out.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp Reasonix MCP config %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp Reasonix MCP config %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace Reasonix MCP config %s: %w", path, err)
	}
	return nil
}

// Static interface assertions — ensure Agent remains compliant with core.Agent.
var _ core.Agent = (*Agent)(nil)              // *Agent satisfies core.Agent
var _ core.ModeSwitcher = (*Agent)(nil)       // mode switching
var _ core.WorkDirSwitcher = (*Agent)(nil)    // work dir switching
var _ core.ContextCompressor = (*Agent)(nil)  // compact support
var _ core.MemoryFileProvider = (*Agent)(nil) // memory file support
