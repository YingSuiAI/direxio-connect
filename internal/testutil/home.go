package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// IsolatedHome redirects every home/config root used by repository tests to a
// fresh temporary directory. Tests that need a runtime-specific override may
// replace the corresponding environment variable after calling this helper.
func IsolatedHome(t testing.TB) string {
	t.Helper()
	return SetIsolatedHome(t, t.TempDir())
}

// SetIsolatedHome redirects every home/config root used by repository tests to
// root. The root must be absolute so accidental fallback paths are detectable.
func SetIsolatedHome(t testing.TB, root string) string {
	t.Helper()

	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve isolated home %q: %v", root, err)
	}
	absRoot = filepath.Clean(absRoot)
	if err := os.MkdirAll(absRoot, 0o700); err != nil {
		t.Fatalf("create isolated home %q: %v", absRoot, err)
	}

	configRoot := filepath.Join(absRoot, ".config")
	dataRoot := filepath.Join(absRoot, ".local", "share")
	appData := filepath.Join(absRoot, "AppData", "Roaming")
	localAppData := filepath.Join(absRoot, "AppData", "Local")
	for _, dir := range []string{configRoot, dataRoot, appData, localAppData} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create isolated config root %q: %v", dir, err)
		}
	}

	t.Setenv("HOME", absRoot)
	t.Setenv("USERPROFILE", absRoot)
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(absRoot)
		t.Setenv("HOMEDRIVE", volume)
		t.Setenv("HOMEPATH", strings.TrimPrefix(absRoot, volume))
	} else {
		t.Setenv("HOMEDRIVE", "")
		t.Setenv("HOMEPATH", "")
	}
	t.Setenv("APPDATA", appData)
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_DATA_HOME", dataRoot)
	t.Setenv("DIREXTALK_HOME", filepath.Join(absRoot, ".dirextalk"))

	// Runtime-specific roots must never inherit a developer's real settings.
	t.Setenv("CODEX_HOME", filepath.Join(absRoot, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(absRoot, ".claude"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(absRoot, ".pi", "agent"))
	t.Setenv("GEMINI_CLI_SYSTEM_SETTINGS_PATH", filepath.Join(absRoot, ".gemini", "system-settings.json"))
	t.Setenv("CC_CONFIG_PATH", filepath.Join(absRoot, ".cc-connect", "config.toml"))
	t.Setenv("CC_DATA_DIR", filepath.Join(absRoot, ".cc-connect"))

	return absRoot
}

// WithinRoot reports whether target is root itself or a descendant of root.
func WithinRoot(root, target string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(absRoot), filepath.Clean(absTarget))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// AssertWithinRoot fails the test before a writer can target a path outside
// the explicitly isolated root.
func AssertWithinRoot(t testing.TB, root, target string) {
	t.Helper()
	if !WithinRoot(root, target) {
		t.Fatalf("test write target %q escapes isolated root %q", target, root)
	}
}
