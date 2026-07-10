//go:build !windows

package securetemp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecureTempMCPFileUsesOwnerOnlyPOSIXPermissions(t *testing.T) {
	path, cleanup, err := WriteFile(t.TempDir(), "dirextalk-secure-", "mcp.json", []byte("Bearer test-token"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	defer cleanup()

	for _, check := range []struct {
		path string
		want os.FileMode
	}{
		{path: filepath.Dir(path), want: 0o700},
		{path: path, want: 0o600},
	} {
		info, err := os.Stat(check.path)
		if err != nil {
			t.Fatalf("Stat(%q): %v", check.path, err)
		}
		if got := info.Mode().Perm(); got != check.want {
			t.Fatalf("mode(%q) = %o, want %o", check.path, got, check.want)
		}
	}
}
