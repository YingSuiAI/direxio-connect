package sentinel_guard_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSafeSentinelPathGuard(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh is not available")
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	guard := filepath.Join(repoRoot, "scripts", "Assert-SafeSentinelPath.ps1")
	tempRoot := filepath.Clean(os.TempDir())
	safeSentinel := filepath.Join(tempRoot, "dirextalk-sentinel-guard-safe")

	t.Run("accepts canonical TempDir child", func(t *testing.T) {
		cmd := exec.Command("pwsh", "-NoProfile", "-NonInteractive", "-File", guard,
			"-SentinelHome", safeSentinel, "-TempRoot", tempRoot)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("safe sentinel rejected: %v\n%s", err, out)
		}
	})

	t.Run("rejects empty sentinel", func(t *testing.T) {
		cmd := exec.Command("pwsh", "-NoProfile", "-NonInteractive", "-File", guard,
			"-SentinelHome", "", "-TempRoot", tempRoot)
		if err := cmd.Run(); err == nil {
			t.Fatal("empty SentinelHome unexpectedly accepted")
		}
	})

	t.Run("rejects unset sentinel", func(t *testing.T) {
		cmd := exec.Command("pwsh", "-NoProfile", "-NonInteractive", "-File", guard,
			"-TempRoot", tempRoot)
		if err := cmd.Run(); err == nil {
			t.Fatal("unset SentinelHome unexpectedly accepted")
		}
	})

	t.Run("rejects current profile", func(t *testing.T) {
		profile := t.TempDir()
		cmd := exec.Command("pwsh", "-NoProfile", "-NonInteractive", "-File", guard,
			"-SentinelHome", profile, "-TempRoot", tempRoot)
		cmd.Env = replaceEnv(os.Environ(), map[string]string{
			"HOME":        profile,
			"USERPROFILE": profile,
		})
		if err := cmd.Run(); err == nil {
			t.Fatal("current profile unexpectedly accepted as SentinelHome")
		}
	})

	t.Run("lowercase home assignment hard fails", func(t *testing.T) {
		command := strings.Join([]string{
			"Set-StrictMode -Version Latest",
			"$ErrorActionPreference = 'Stop'",
			"$home = '" + strings.ReplaceAll(safeSentinel, "'", "''") + "'",
			"throw 'lowercase $home assignment unexpectedly succeeded'",
		}, "; ")
		cmd := exec.Command("pwsh", "-NoProfile", "-NonInteractive", "-Command", command)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("assignment to lowercase $home unexpectedly succeeded")
		}
		if strings.Contains(string(out), "lowercase $home assignment unexpectedly succeeded") {
			t.Fatalf("lowercase $home assignment did not hard-fail:\n%s", out)
		}
	})
}

func replaceEnv(base []string, replacements map[string]string) []string {
	out := make([]string, 0, len(base)+len(replacements))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replaced := replacements[strings.ToUpper(key)]; replaced {
				continue
			}
		}
		out = append(out, entry)
	}
	for key, value := range replacements {
		out = append(out, key+"="+value)
	}
	return out
}
