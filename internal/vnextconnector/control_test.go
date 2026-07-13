package vnextconnector

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"

	controlv1 "github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector/agentcontrolv1"
)

const (
	testHostID  = "01890a5d-ac96-7c84-bc6a-9c5b51c7e333"
	testBootID  = "01890a5d-ac96-7c84-bc6a-9c5b51c7e444"
	testLeaseID = "01890a5d-ac96-7c84-bc6a-9c5b51c7e555"
)

func TestControlSessionCommitsCommandBeforeAcknowledgementAndStops(t *testing.T) {
	identity := StateIdentity{
		TenantID:            testTenantID,
		ConnectorID:         testConnectorID,
		ConnectorGeneration: 7,
	}
	store, err := NewStateStore(filepath.Join(t.TempDir(), "connector-state.json"), identity)
	if err != nil {
		t.Fatal(err)
	}
	state := State{
		SchemaVersion:         CurrentStateSchemaVersion,
		TenantID:              testTenantID,
		ConnectorID:           testConnectorID,
		ConnectorGeneration:   7,
		AppliedConfigRevision: 12,
		DesiredState:          DesiredStateRunning,
		AdapterConfig:         map[string]string{"adapter": "codex-app-server"},
		RuntimeConfig:         map[string]string{"offline-policy": "queue"},
		StableErrorCode:       stablePayloadUnavailable,
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	buildDigest := sha256.Sum256([]byte("test adapter build"))
	client, err := NewClient(ClientConfig{
		TenantID:              testTenantID,
		ConnectorID:           testConnectorID,
		HostID:                testHostID,
		BootID:                testBootID,
		ConnectorGeneration:   7,
		SpecRevision:          12,
		RuntimeKind:           "codex",
		RuntimeVersion:        "test",
		Adapter:               "codex-app-server",
		Profile:               "default",
		OfflinePolicy:         "queue",
		AdapterBuildDigest:    buildDigest,
		MaximumConcurrentRuns: 2,
		MaximumQueueDepth:     8,
		StateStore:            store,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := &scriptedControlStream{receive: []*controlv1.ServerFrame{
		testConnectLeaseFrame(0),
		{Kind: &controlv1.ServerFrame_DurableCommand{DurableCommand: applyConfigFrameForState(
			t,
			1,
			controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_STOPPED,
		)}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state, err = client.runSession(ctx, stream, state)
	if !errors.Is(err, errConnectorStopped) {
		t.Fatalf("runSession error = %v, want stopped", err)
	}
	if state.AppliedCommandSequence != 1 || state.AppliedConfigRevision != 13 || state.DesiredState != DesiredStateStopped {
		t.Fatalf("returned state = %+v", state)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.AppliedCommandSequence != 1 || persisted.DesiredState != DesiredStateStopped {
		t.Fatalf("persisted state = %+v", persisted)
	}
	if got := client.capacity(state).MaximumConcurrentRuns; got != 3 {
		t.Fatalf("effective maximum concurrent runs = %d, want durable value 3", got)
	}

	sent := stream.sentFrames()
	if len(sent) != 3 || sent[0].GetHello() == nil || sent[1].GetReady() == nil || sent[2].GetCommandAcknowledgement() == nil {
		t.Fatalf("sent frames = %#v", sent)
	}
	ack := sent[2].GetCommandAcknowledgement()
	if ack.CommandSequence != persisted.AppliedCommandSequence || len(ack.EncodedCommandDigest) != sha256.Size || len(ack.PayloadDigest) != sha256.Size {
		t.Fatalf("command ACK = %+v", ack)
	}
}

func TestControlSessionReplaysCommittedStopBeforeExiting(t *testing.T) {
	identity := StateIdentity{TenantID: testTenantID, ConnectorID: testConnectorID, ConnectorGeneration: 7}
	store, err := NewStateStore(filepath.Join(t.TempDir(), "connector-state.json"), identity)
	if err != nil {
		t.Fatal(err)
	}
	frame := applyConfigFrameForState(t, 1, controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_STOPPED)
	verified, err := VerifyDurableCommand(frame, CommandIdentity{ConnectorGeneration: 7, SpecRevision: 12}, CommandCursor{})
	if err != nil {
		t.Fatal(err)
	}
	state := State{
		SchemaVersion:            CurrentStateSchemaVersion,
		TenantID:                 testTenantID,
		ConnectorID:              testConnectorID,
		ConnectorGeneration:      7,
		AppliedConfigRevision:    13,
		AppliedCommandSequence:   1,
		LastEncodedCommandDigest: append(StateDigest(nil), verified.EncodedDigest[:]...),
		LastPayloadDigest:        append(StateDigest(nil), verified.PayloadDigest[:]...),
		DesiredState:             DesiredStateStopped,
		AdapterConfig:            cloneStringMap(verified.AdapterConfig),
		RuntimeConfig:            cloneStringMap(verified.RuntimeConfig),
		StableErrorCode:          stablePayloadUnavailable,
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	buildDigest := sha256.Sum256([]byte("test adapter build"))
	client, err := NewClient(ClientConfig{
		TenantID: testTenantID, ConnectorID: testConnectorID, HostID: testHostID, BootID: testBootID,
		ConnectorGeneration: 7, SpecRevision: 12, RuntimeKind: "codex", RuntimeVersion: "test",
		Adapter: "codex-app-server", Profile: "default", OfflinePolicy: "queue",
		AdapterBuildDigest: buildDigest, MaximumConcurrentRuns: 2, MaximumQueueDepth: 8, StateStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := &scriptedControlStream{receive: []*controlv1.ServerFrame{
		testConnectLeaseFrame(0),
		{Kind: &controlv1.ServerFrame_DurableCommand{DurableCommand: frame}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = client.runSession(ctx, stream, state)
	if !errors.Is(err, errConnectorStopped) {
		t.Fatalf("runSession error = %v, want stopped after replay", err)
	}
	sent := stream.sentFrames()
	if len(sent) != 2 || sent[1].GetCommandAcknowledgement() == nil || sent[1].GetCommandAcknowledgement().CommandSequence != 1 {
		t.Fatalf("crash replay did not send exact ACK: %#v", sent)
	}
	if sent[0].GetHello() == nil || sent[0].GetHello().SpecRevision != 13 {
		t.Fatalf("reconnect Hello did not advertise durable revision: %#v", sent[0])
	}
}

func TestControlTargetAcceptsOnlyHTTPSOrigin(t *testing.T) {
	if got, err := controlTarget("https://control.example.com:8443"); err != nil || got != "dns:///control.example.com:8443" {
		t.Fatalf("controlTarget = %q, %v", got, err)
	}
	for _, value := range []string{
		"http://control.example.com",
		"https://user@control.example.com",
		"https://control.example.com/path",
		"https://control.example.com?tenant=one",
	} {
		if _, err := controlTarget(value); err == nil {
			t.Fatalf("controlTarget(%q) unexpectedly succeeded", value)
		}
	}
}

func testConnectLeaseFrame(acknowledgedCommandSequence uint64) *controlv1.ServerFrame {
	return &controlv1.ServerFrame{Kind: &controlv1.ServerFrame_ConnectLease{ConnectLease: &controlv1.ConnectLease{
		Fence: &controlv1.LeaseFence{
			TenantId: testTenantID, ConnectorId: testConnectorID, BootId: testBootID,
			ConnectorGeneration: 7, LeaseId: testLeaseID, LeaseEpoch: 1,
		},
		ProtocolMajor: 1, ProtocolMinor: 1, IssuedAtMillis: 1_000, ExpiresAtMillis: 4_000,
		HeartbeatIntervalMillis: 1_000, HeartbeatTtlMillis: 3_000,
		AcknowledgedCommandSequence: acknowledgedCommandSequence,
	}}}
}

type scriptedControlStream struct {
	mu      sync.Mutex
	receive []*controlv1.ServerFrame
	sent    []*controlv1.ClientFrame
}

func (stream *scriptedControlStream) Send(frame *controlv1.ClientFrame) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.sent = append(stream.sent, frame)
	return nil
}

func (stream *scriptedControlStream) Recv() (*controlv1.ServerFrame, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.receive) == 0 {
		return nil, io.EOF
	}
	frame := stream.receive[0]
	stream.receive = stream.receive[1:]
	return frame, nil
}

func (stream *scriptedControlStream) sentFrames() []*controlv1.ClientFrame {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return append([]*controlv1.ClientFrame(nil), stream.sent...)
}
