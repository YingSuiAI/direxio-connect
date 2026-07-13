package agentcontrolv1

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

const frozenSourceSHA256 = "1f0443733cf3fecb9d79c602c9788b464912b3bddf4f8e67c2dc89bd14c1d6ab"

func TestFrozenAgentControlSourceIsExact(t *testing.T) {
	source, err := os.ReadFile("agent_control.proto")
	if err != nil {
		t.Fatalf("read frozen agent control source: %v", err)
	}

	digest := sha256.Sum256(source)
	if got := hex.EncodeToString(digest[:]); got != frozenSourceSHA256 {
		t.Fatalf("frozen agent control source SHA-256 = %s, want %s", got, frozenSourceSHA256)
	}
}
