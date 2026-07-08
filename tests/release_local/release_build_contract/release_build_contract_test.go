package release_build_contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

func TestReleaseDocsDoNotNarrowGenericBuildToCodex(t *testing.T) {
	root := repoRoot(t)
	codexOnlyBuild := "make build AGENTS=" + "codex"
	files := []string{
		"AGENTS.md",
		"README.md",
		"README.zh-CN.md",
		"INSTALL.md",
		"config.example.toml",
		filepath.Join("docs", "matrix.md"),
		filepath.Join("docs", "matrix.zh-CN.md"),
	}
	for _, rel := range files {
		t.Run(rel, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			if strings.Contains(string(body), codexOnlyBuild) {
				t.Fatalf("%s documents a Codex-only build; generic releases must include all supported agent backends", rel)
			}
		})
	}
}

func TestDefaultReleaseBuildIncludesGenericAgentBackends(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "list", "-f", "{{join .GoFiles \"\\n\"}}", "-tags", "no_web goolm", "./cmd/cc-connect")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list release files: %v\n%s", err, out)
	}
	files := string(out)
	for _, want := range []string{
		"plugin_agent_acp.go",
		"plugin_agent_claudecode.go",
		"plugin_agent_codex.go",
		"plugin_agent_gemini.go",
		"plugin_agent_opencode.go",
		"plugin_agent_qoder.go",
	} {
		if !strings.Contains(files, want) {
			t.Fatalf("default release build missing %s; files:\n%s", want, files)
		}
	}
}
