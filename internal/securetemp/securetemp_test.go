package securetemp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecureTempMCPWriteFileCreatesPrivateTreeAndCleansItIdempotently(t *testing.T) {
	root := t.TempDir()
	path, cleanup, err := WriteFile(root, "dirextalk-secure-", "mcp.json", []byte("Bearer test-token"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("path %q is not a strict child of root %q (rel=%q, err=%v)", path, root, rel, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "Bearer test-token" {
		t.Fatalf("ReadFile(%q) = %q, %v", path, got, err)
	}

	dir := filepath.Dir(path)
	cleanup()
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("secure temporary directory survived cleanup: %v", err)
	}
}

func TestSecureTempMCPWriteFileRejectsNestedFileNameWithoutLeavingTemporaryDirectory(t *testing.T) {
	root := t.TempDir()
	_, _, err := WriteFile(root, "dirextalk-secure-", filepath.Join("nested", "mcp.json"), []byte("Bearer test-token"))
	if err == nil {
		t.Fatal("WriteFile accepted a nested file name")
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary artifacts survived rejected file name: %v", entries)
	}
}
