package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsolatedHomeRedirectsUserAndConfigRoots(t *testing.T) {
	root := IsolatedHome(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if filepath.Clean(home) != filepath.Clean(root) {
		t.Fatalf("UserHomeDir = %q, want isolated root %q", home, root)
	}

	for _, name := range []string{
		"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME",
		"XDG_DATA_HOME", "DIREXTALK_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR",
		"PI_CODING_AGENT_DIR", "GEMINI_CLI_SYSTEM_SETTINGS_PATH", "CC_CONFIG_PATH", "CC_DATA_DIR",
	} {
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("%s is empty", name)
		}
		if !WithinRoot(root, value) {
			t.Fatalf("%s = %q escapes isolated root %q", name, value, root)
		}
	}
}

func TestWithinRootRejectsSiblingAndTraversal(t *testing.T) {
	root := t.TempDir()
	if !WithinRoot(root, filepath.Join(root, "nested", "config.json")) {
		t.Fatal("nested path should be accepted")
	}
	if WithinRoot(root, filepath.Join(root, "..", "outside", "config.json")) {
		t.Fatal("traversal path should be rejected")
	}
	if WithinRoot(root, root+"-sibling") {
		t.Fatal("sibling path with common prefix should be rejected")
	}
}
