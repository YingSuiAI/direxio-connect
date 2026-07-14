package vnextconnector

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	controlv1 "github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector/agentcontrolv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// This fixture is copied from the Rust-owned source of truth at
// dirextalk-vnext-server/crates/dtx-agent-control/test-vectors/enrollment-request-v1.json.
type enrollmentRequestVector struct {
	Schema              string `json:"schema"`
	Version             uint16 `json:"version"`
	TokenHex            string `json:"token_hex"`
	TenantID            string `json:"tenant_id"`
	HostID              string `json:"host_id"`
	ConnectorID         string `json:"connector_id"`
	Generation          uint64 `json:"generation"`
	SpecRevision        uint64 `json:"spec_revision"`
	RequestID           string `json:"request_id"`
	ControlSeedHex      string `json:"control_seed_hex"`
	RefreshSeedHex      string `json:"refresh_seed_hex"`
	ControlPublicKeyHex string `json:"control_public_key_hex"`
	RefreshPublicKeyHex string `json:"refresh_public_key_hex"`
	TokenDigestHex      string `json:"token_digest_hex"`
	SigningBytesHex     string `json:"signing_bytes_hex"`
	ControlSignatureHex string `json:"control_signature_hex"`
	RefreshSignatureHex string `json:"refresh_signature_hex"`
	RequestDigestHex    string `json:"request_digest_hex"`
}

func TestEnrollmentRequestMatchesRustOwnedVector(t *testing.T) {
	var vector enrollmentRequestVector
	contents, err := os.ReadFile("testdata/enrollment-request-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &vector); err != nil {
		t.Fatal(err)
	}
	if vector.Schema != "dirextalk.connector-enrollment-request-vector" || vector.Version != 1 {
		t.Fatalf("unexpected vector identity: %q v%d", vector.Schema, vector.Version)
	}
	token := mustDecodeEnrollmentHex(t, vector.TokenHex)
	entropy := append(mustDecodeEnrollmentHex(t, vector.ControlSeedHex), mustDecodeEnrollmentHex(t, vector.RefreshSeedHex)...)
	options := enrollmentTestOptions()
	options.TenantID = vector.TenantID
	options.HostID = vector.HostID
	options.ConnectorID = vector.ConnectorID
	options.Generation = vector.Generation
	options.SpecRevision = vector.SpecRevision
	options.RequestID = vector.RequestID

	request, controlPrivate, refreshPrivate, tokenDigest, signingBytes, requestDigest, err := buildEnrollmentRequest(options, token, bytes.NewReader(entropy))
	if err != nil {
		t.Fatalf("build enrollment request: %v", err)
	}
	defer clear(controlPrivate)
	defer clear(refreshPrivate)
	checks := map[string][]byte{
		"control public key": request.ControlPublicKey,
		"refresh public key": request.RefreshPublicKey,
		"token digest":       tokenDigest,
		"signing bytes":      signingBytes,
		"control signature":  request.ControlSignature,
		"refresh signature":  request.RefreshSignature,
		"request digest":     requestDigest,
	}
	expected := map[string]string{
		"control public key": vector.ControlPublicKeyHex,
		"refresh public key": vector.RefreshPublicKeyHex,
		"token digest":       vector.TokenDigestHex,
		"signing bytes":      vector.SigningBytesHex,
		"control signature":  vector.ControlSignatureHex,
		"refresh signature":  vector.RefreshSignatureHex,
		"request digest":     vector.RequestDigestHex,
	}
	for name, actual := range checks {
		if hex.EncodeToString(actual) != expected[name] {
			t.Fatalf("%s did not match Rust vector", name)
		}
	}
}

func TestEnrollConnectorRetriesExactRequestAndBuildsLoadableCredential(t *testing.T) {
	options := enrollmentTestOptions()
	token := bytes.Repeat([]byte{0x42}, enrollmentTokenBytes)
	controlRootPEM, signer := enrollmentControlRoot(t)
	fake := &enrollmentRPCStub{respond: func(request *controlv1.EnrollConnectorRequest) *controlv1.EnrollConnectorResponse {
		leafDER, notBefore, notAfter := enrollmentLeaf(t, request, signer)
		fingerprint := sha256.Sum256(leafDER)
		credential := &controlv1.ConnectorCredential{
			CredentialId:        "01890f47-5fd4-7cc2-8f8f-5f9476f4f006",
			CredentialRevision:  1,
			CertificateChainDer: [][]byte{leafDER},
			LeafFingerprint:     fingerprint[:],
			ValidFromMillis:     uint64(notBefore.UnixMilli()),
			ValidUntilMillis:    uint64(notAfter.UnixMilli()),
		}
		return &controlv1.EnrollConnectorResponse{
			Credential:    credential,
			RequestDigest: enrollmentRequestDigest(request),
			ResultDigest:  enrollmentCredentialResultDigest(options, request, credential),
		}
	}}
	entropy := append(bytes.Repeat([]byte{0x11}, ed25519.SeedSize), bytes.Repeat([]byte{0x22}, ed25519.SeedSize)...)
	credentialJSON, err := enrollConnectorWithRPC(
		context.Background(), options, token, controlRootPEM, fake, bytes.NewReader(entropy),
		func(context.Context, time.Duration) error { return nil },
	)
	if err != nil {
		t.Fatalf("enroll Connector: %v", err)
	}
	defer clear(credentialJSON)
	if fake.calls != 2 {
		t.Fatalf("enrollment calls = %d, want one retry", fake.calls)
	}
	if !reflect.DeepEqual(fake.requests[0], fake.requests[1]) {
		t.Fatal("retry changed the signed enrollment request")
	}
	credential, err := validateControlCredentialDocument(
		credentialJSON, options.TenantID, options.ConnectorID, options.Generation, options.ControlURL,
	)
	if err != nil {
		t.Fatalf("generated credential did not pass supervisor loader: %v", err)
	}
	if credential.CredentialRevision != 1 || credential.ServerName != options.ControlServerName {
		t.Fatalf("unexpected loaded credential: %+v", credential)
	}
	if bytes.Contains(credentialJSON, []byte("refresh")) {
		t.Fatal("schema-v1 output must not pretend refresh-key persistence is supported")
	}
}

func TestEnrollmentRPCErrorDoesNotExposeExternalStatusDetail(t *testing.T) {
	client := permanentEnrollmentErrorRPC{}
	_, err := callEnrollmentWithRetry(
		context.Background(),
		client,
		&controlv1.EnrollConnectorRequest{},
		func(context.Context, time.Duration) error { return nil },
	)
	if err == nil {
		t.Fatal("permanent enrollment error was accepted")
	}
	if strings.Contains(err.Error(), "sensitive-external-detail") {
		t.Fatalf("external gRPC status detail leaked: %v", err)
	}
	if !strings.Contains(err.Error(), codes.PermissionDenied.String()) {
		t.Fatalf("stable gRPC status code missing: %v", err)
	}
}

type enrollmentRPCStub struct {
	calls    int
	requests [][]byte
	respond  func(*controlv1.EnrollConnectorRequest) *controlv1.EnrollConnectorResponse
}

type permanentEnrollmentErrorRPC struct{}

func (permanentEnrollmentErrorRPC) EnrollConnector(
	context.Context,
	*controlv1.EnrollConnectorRequest,
	...grpc.CallOption,
) (*controlv1.EnrollConnectorResponse, error) {
	return nil, status.Error(codes.PermissionDenied, "sensitive-external-detail")
}

func (stub *enrollmentRPCStub) EnrollConnector(
	_ context.Context,
	request *controlv1.EnrollConnectorRequest,
	_ ...grpc.CallOption,
) (*controlv1.EnrollConnectorResponse, error) {
	stub.calls++
	encoded, err := proto.Marshal(request)
	if err != nil {
		return nil, err
	}
	stub.requests = append(stub.requests, encoded)
	if stub.calls == 1 {
		return nil, status.Error(codes.Unavailable, "retry exact request")
	}
	return stub.respond(request), nil
}

func enrollmentTestOptions() EnrollmentOptions {
	return EnrollmentOptions{
		TenantID:             "01890f47-5fd4-7cc2-8f8f-5f9476f4f002",
		HostID:               "01890f47-5fd4-7cc2-8f8f-5f9476f4f003",
		ConnectorID:          "01890f47-5fd4-7cc2-8f8f-5f9476f4f004",
		Generation:           1,
		SpecRevision:         1,
		RequestID:            "01890f47-5fd4-7cc2-8f8f-5f9476f4f005",
		EnrollmentURL:        "https://enroll.example.test:9443",
		EnrollmentServerName: "enroll.example.test",
		EnrollmentRootCAFile: "enrollment-ca.pem",
		ControlURL:           "https://control.example.test:9444",
		ControlServerName:    "control.example.test",
		ControlRootCAFile:    "control-ca.pem",
	}
}

func enrollmentControlRoot(t *testing.T) ([]byte, *enrollmentCertificateSigner) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Dirextalk test"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(2 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), &enrollmentCertificateSigner{certificate, privateKey}
}

type enrollmentCertificateSigner struct {
	certificate *x509.Certificate
	privateKey  ed25519.PrivateKey
}

func enrollmentLeaf(
	t *testing.T,
	request *controlv1.EnrollConnectorRequest,
	signer *enrollmentCertificateSigner,
) ([]byte, time.Time, time.Time) {
	t.Helper()
	identity, err := url.Parse("spiffe://dirextalk.internal/v1/tenants/" + request.TenantId + "/connectors/" + request.ConnectorId)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	notBefore, notAfter := now.Add(-time.Minute), now.Add(time.Hour)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:                  []*url.URL{identity},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, signer.certificate, ed25519.PublicKey(request.ControlPublicKey), signer.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return der, notBefore, notAfter
}

func mustDecodeEnrollmentHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
