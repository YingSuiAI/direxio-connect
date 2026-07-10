package line_endings_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTrackedTextLFGateUsesGitBinaryClassification(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh is not available")
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	gate := filepath.Join(repoRoot, "scripts", "Test-TrackedTextLF.ps1")

	fixture := t.TempDir()
	runGit(t, fixture, "init", "--quiet")
	runGit(t, fixture, "config", "core.autocrlf", "false")
	runGit(t, fixture, "config", "core.safecrlf", "false")

	for name, content := range map[string][]byte{
		"photo sample.jpeg": {'J', 'P', 'E', 'G', 0, '\r', '\n'},
		"texture.webp":      {'W', 'E', 'B', 'P', 0, '\r', '\n'},
		"font.woff2":        {'w', 'O', 'F', '2', 0, '\r', '\n'},
		"bundle.zip":        {'P', 'K', 3, 4, 0, '\r', '\n'},
	} {
		writeFixture(t, fixture, name, content)
		runGit(t, fixture, "add", "--", name)
	}
	for _, binaryName := range []string{"photo sample.jpeg", "texture.webp", "font.woff2", "bundle.zip"} {
		eol := runGitOutput(t, fixture, "ls-files", "--eol", "--", binaryName)
		if !strings.Contains(eol, "i/-text") {
			t.Fatalf("unattributed binary %q was not classified as i/-text: %q", binaryName, eol)
		}
	}

	if out, err := runGate(gate, fixture); err != nil {
		t.Fatalf("binary-only fixture failed LF gate: %v\n%s", err, out)
	}

	writeFixture(t, fixture, "worktree CRLF.txt", []byte("first\nsecond\n"))
	runGit(t, fixture, "add", "--", "worktree CRLF.txt")
	writeFixture(t, fixture, "worktree CRLF.txt", []byte("first\r\nsecond\r\n"))
	eol := runGitOutput(t, fixture, "ls-files", "--eol", "--", "worktree CRLF.txt")
	if !strings.Contains(eol, "i/lf") || !strings.Contains(eol, "w/crlf") {
		t.Fatalf("fixture did not produce i/lf+w/crlf metadata: %q", eol)
	}
	out, err := runGate(gate, fixture)
	if err == nil {
		t.Fatalf("working-tree CRLF text unexpectedly passed LF gate:\n%s", out)
	}
	if !strings.Contains(out, "worktree CRLF.txt") || !strings.Contains(out, "working tree") {
		t.Fatalf("LF gate did not identify i/lf+w/crlf text file:\n%s", out)
	}
	for _, binaryName := range []string{"photo sample.jpeg", "texture.webp", "font.woff2", "bundle.zip"} {
		if strings.Contains(out, binaryName) {
			t.Fatalf("LF gate misclassified binary %q:\n%s", binaryName, out)
		}
	}

	writeFixture(t, fixture, "worktree CRLF.txt", []byte("first\nsecond\n"))
	writeFixture(t, fixture, "index CRLF.txt", []byte("first\r\nsecond\r\n"))
	runGit(t, fixture, "add", "--", "worktree CRLF.txt", "index CRLF.txt")
	out, err = runGate(gate, fixture)
	if err == nil || !strings.Contains(out, "index CRLF.txt") || !strings.Contains(out, "Git index") {
		t.Fatalf("index CRLF text was not rejected:\n%s", out)
	}
}

func runGate(script, repo string) (string, error) {
	cmd := exec.Command("pwsh", "-NoProfile", "-File", script, "-RepositoryRoot", repo)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = runGitOutput(t, dir, args...)
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFixture(t *testing.T, root, name string, content []byte) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}
