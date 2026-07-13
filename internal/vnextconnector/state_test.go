package vnextconnector

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	testTenantID    = "01890a5d-ac96-7c84-bc6a-9c5b51c7e111"
	testConnectorID = "01890a5d-ac96-7c84-bc6a-9c5b51c7e222"
)

func TestStateStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connector-state.json")
	identity := StateIdentity{
		TenantID:            testTenantID,
		ConnectorID:         testConnectorID,
		ConnectorGeneration: 7,
	}
	store, err := NewStateStore(path, identity)
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}

	want := validState(identity)
	if err := store.Save(want); err != nil {
		t.Fatalf("save state: %v", err)
	}

	// Exercise replacement as well as first creation.
	want.StableErrorCode = "RUNTIME_BUSY"
	if err := store.Save(want); err != nil {
		t.Fatalf("replace state: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !statesEqual(got, want) {
		t.Fatalf("loaded state = %#v, want %#v", got, want)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read encoded state: %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"last_encoded_command_digest":"1111111111111111111111111111111111111111111111111111111111111111"`)) {
		t.Fatalf("encoded state does not contain lowercase hexadecimal digest: %s", encoded)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat state: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("state mode = %04o, want 0600", got)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".connector-state.json.tmp-*")); err != nil {
		t.Fatalf("glob temporary files: %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("temporary state files remain: %v", matches)
	}
}

func TestStateStoreRejectsCorruptAndOversizeFiles(t *testing.T) {
	identity := StateIdentity{TenantID: testTenantID, ConnectorID: testConnectorID, ConnectorGeneration: 7}
	validEncoded, err := json.Marshal(validState(identity))
	if err != nil {
		t.Fatalf("encode valid state: %v", err)
	}
	duplicateIdentity := bytes.Replace(validEncoded, []byte(`"tenant_id":`), []byte(`"tenant_id":"duplicate","tenant_id":`), 1)
	uppercaseDigest := bytes.Replace(
		validEncoded,
		[]byte(strings.Repeat("11", 32)),
		[]byte(strings.Repeat("AA", 32)),
		1,
	)

	tests := []struct {
		name string
		data []byte
		want error
	}{
		{name: "invalid json", data: []byte("{"), want: ErrInvalidState},
		{name: "unknown field", data: []byte(`{"schema_version":1,"unexpected":true}`), want: ErrInvalidState},
		{name: "duplicate identity", data: duplicateIdentity, want: ErrInvalidState},
		{name: "uppercase digest", data: uppercaseDigest, want: ErrInvalidState},
		{name: "trailing value", data: []byte(`{} {}`), want: ErrInvalidState},
		{name: "oversize", data: bytes.Repeat([]byte("x"), MaxStateJSONBytes+1), want: ErrStateTooLarge},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "connector-state.json")
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			store, err := NewStateStore(path, identity)
			if err != nil {
				t.Fatalf("new state store: %v", err)
			}
			if _, err := store.Load(); !errors.Is(err, test.want) {
				t.Fatalf("load error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestStateStoreRejectsSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real-state.json")
	linkPath := filepath.Join(dir, "connector-state.json")
	original := []byte("do not replace")
	if err := os.WriteFile(realPath, original, 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	identity := StateIdentity{TenantID: testTenantID, ConnectorID: testConnectorID, ConnectorGeneration: 7}
	store, err := NewStateStore(linkPath, identity)
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrStateSymlink) {
		t.Fatalf("load error = %v, want %v", err, ErrStateSymlink)
	}
	if err := store.Save(validState(identity)); !errors.Is(err, ErrStateSymlink) {
		t.Fatalf("save error = %v, want %v", err, ErrStateSymlink)
	}
	if got, err := os.ReadFile(realPath); err != nil {
		t.Fatalf("read symlink target: %v", err)
	} else if !bytes.Equal(got, original) {
		t.Fatalf("symlink target changed: %q", got)
	}
}

func TestStateStoreBindsCursorToIdentityAndGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connector-state.json")
	identity := StateIdentity{TenantID: testTenantID, ConnectorID: testConnectorID, ConnectorGeneration: 7}
	store, err := NewStateStore(path, identity)
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}
	if err := store.Save(validState(identity)); err != nil {
		t.Fatalf("save state: %v", err)
	}

	otherIdentity := identity
	otherIdentity.ConnectorID = "01890a5d-ac96-7c84-bc6a-9c5b51c7e333"
	otherStore, err := NewStateStore(path, otherIdentity)
	if err != nil {
		t.Fatalf("new identity-mismatch store: %v", err)
	}
	if _, err := otherStore.Load(); !errors.Is(err, ErrStateIdentityMismatch) {
		t.Fatalf("identity mismatch error = %v, want %v", err, ErrStateIdentityMismatch)
	}

	otherGeneration := identity
	otherGeneration.ConnectorGeneration++
	generationStore, err := NewStateStore(path, otherGeneration)
	if err != nil {
		t.Fatalf("new generation-mismatch store: %v", err)
	}
	if _, err := generationStore.Load(); !errors.Is(err, ErrStateGenerationMismatch) {
		t.Fatalf("generation mismatch error = %v, want %v", err, ErrStateGenerationMismatch)
	}

	changed := validState(identity)
	changed.AppliedCommandSequence = 0
	changed.LastEncodedCommandDigest = nil
	changed.LastPayloadDigest = nil
	changed.ConnectorGeneration++
	if err := store.Save(changed); !errors.Is(err, ErrStateGenerationMismatch) {
		t.Fatalf("save generation mismatch error = %v, want %v", err, ErrStateGenerationMismatch)
	}
}

func validState(identity StateIdentity) State {
	return State{
		SchemaVersion:            CurrentStateSchemaVersion,
		TenantID:                 identity.TenantID,
		ConnectorID:              identity.ConnectorID,
		ConnectorGeneration:      identity.ConnectorGeneration,
		AppliedConfigRevision:    4,
		AppliedCommandSequence:   9,
		LastEncodedCommandDigest: StateDigest(bytes.Repeat([]byte{0x11}, 32)),
		LastPayloadDigest:        StateDigest(bytes.Repeat([]byte{0x22}, 32)),
		DesiredState:             DesiredStateRunning,
		AdapterConfig: map[string]string{
			"model": "codex",
		},
		RuntimeConfig: map[string]string{
			"maximum-concurrent-runs": "2",
		},
	}
}

func statesEqual(left, right State) bool {
	return left.SchemaVersion == right.SchemaVersion &&
		left.TenantID == right.TenantID &&
		left.ConnectorID == right.ConnectorID &&
		left.ConnectorGeneration == right.ConnectorGeneration &&
		left.AppliedConfigRevision == right.AppliedConfigRevision &&
		left.AppliedCommandSequence == right.AppliedCommandSequence &&
		bytes.Equal(left.LastEncodedCommandDigest, right.LastEncodedCommandDigest) &&
		bytes.Equal(left.LastPayloadDigest, right.LastPayloadDigest) &&
		left.DesiredState == right.DesiredState &&
		stringMapsEqual(left.AdapterConfig, right.AdapterConfig) &&
		stringMapsEqual(left.RuntimeConfig, right.RuntimeConfig) &&
		left.StableErrorCode == right.StableErrorCode
}

func stringMapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
