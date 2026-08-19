package cursor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewResolvesCommandToAbsolutePath(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	gotAgent, err := New(map[string]any{"cmd": executable})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := gotAgent.(*Agent).cmd
	if !filepath.IsAbs(got) {
		t.Fatalf("resolved command = %q, want an absolute path", got)
	}
	if got != executable {
		t.Fatalf("resolved command = %q, want %q", got, executable)
	}
}

func TestParseCursorCmdOptsPreservesExecutablePathWithSpaces(t *testing.T) {
	dir := t.TempDir()
	pathWithSpaces := filepath.Join(dir, "Cursor Agent", "agent")
	if err := os.MkdirAll(filepath.Dir(pathWithSpaces), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(pathWithSpaces, []byte("placeholder"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cmd, args := parseCursorCmdOpts(map[string]any{"cmd": pathWithSpaces + " --print"})
	if cmd != pathWithSpaces {
		t.Fatalf("command = %q, want %q", cmd, pathWithSpaces)
	}
	if len(args) != 1 || args[0] != "--print" {
		t.Fatalf("args = %#v, want [--print]", args)
	}
}

func TestNewMissingCommandUsesOfficialCursorAgentGuidance(t *testing.T) {
	_, err := New(map[string]any{"cmd": "cursor-agent-command-that-is-not-installed"})
	if err == nil {
		t.Fatal("New succeeded for a missing command")
	}
	message := err.Error()
	for _, want := range []string{"official Cursor Agent CLI", "https://cursor.com/install", "absolute path", "launchd"} {
		if !strings.Contains(message, want) {
			t.Errorf("error = %q, missing %q", message, want)
		}
	}
	if strings.Contains(message, "anthropic-ai") || strings.Contains(message, "npm") {
		t.Fatalf("error contains package-manager guidance: %q", message)
	}
}

func TestCursorProcessDiagnosticAuthentication(t *testing.T) {
	err := cursorProcessDiagnostic("/Users/alice/.local/bin/agent", "Authentication required. Please run 'agent login' first.")
	message := err.Error()
	for _, want := range []string{"authentication is required", "/Users/alice/.local/bin/agent login", "CURSOR_API_KEY", "Authentication required"} {
		if !strings.Contains(message, want) {
			t.Errorf("diagnostic = %q, missing %q", message, want)
		}
	}
}

func TestCursorProcessDiagnosticNotReady(t *testing.T) {
	err := cursorProcessDiagnostic("/Users/alice/.local/bin/agent", "Workspace trust required; agent backend offline")
	message := err.Error()
	for _, want := range []string{"is not ready", "/Users/alice/.local/bin/agent status", "trust the workspace", "Workspace trust required"} {
		if !strings.Contains(message, want) {
			t.Errorf("diagnostic = %q, missing %q", message, want)
		}
	}
}
