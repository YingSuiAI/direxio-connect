//go:build unix

package vnextconnector

import (
	"os"
	"testing"
)

func restrictCredentialFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod credential: %v", err)
	}
}
