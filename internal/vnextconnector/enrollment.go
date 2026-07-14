package vnextconnector

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	controlv1 "github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector/agentcontrolv1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

const (
	enrollmentTokenBytes       = 32
	maxEnrollmentCABytes       = 64 * 1024
	maxEnrollmentPathBytes     = 4096
	enrollmentMaximumAttempts  = 3
	enrollmentAttemptTimeout   = 8 * time.Second
	enrollmentInitialRetryWait = 250 * time.Millisecond

	enrollmentTokenDigestDomain   = "dirextalk.connector-enrollment-token.v1"
	enrollmentProofDomain         = "dirextalk.connector-enrollment-proof.v1"
	enrollmentRequestDigestDomain = "dirextalk.connector-enrollment-request.v1"
	enrollmentResultDigestDomain  = "dirextalk.connector-credential-result.v1"
)

var ErrEnrollmentOutcomeUnknown = errors.New("ENROLLMENT_OUTCOME_UNKNOWN")

// EnrollmentOptions binds the one-time enrollment proof to one exact Host,
// Connector fence, request, and independently pinned TLS trust material. The
// schema-v2 result intentionally persists only the control private key;
// refresh-key persistence and rotation remain unsupported.
type EnrollmentOptions struct {
	TenantID                    string
	HostID                      string
	ConnectorID                 string
	Generation                  uint64
	SpecRevision                uint64
	RequestID                   string
	EnrollmentURL               string
	EnrollmentServerName        string
	EnrollmentRootCAFile        string
	EnrollmentRootCASHA256      string
	ControlURL                  string
	ControlServerName           string
	ControlServerRootCAFile     string
	ControlServerRootCASHA256   string
	ConnectorIssuerRootCAFile   string
	ConnectorIssuerRootCASHA256 string
}

type verifiedEnrollmentTrustRoots struct {
	enrollmentRootPEM      []byte
	controlServerRootPEM   []byte
	connectorIssuerRootPEM []byte
}

func (roots *verifiedEnrollmentTrustRoots) clear() {
	if roots == nil {
		return
	}
	clear(roots.enrollmentRootPEM)
	clear(roots.controlServerRootPEM)
	clear(roots.connectorIssuerRootPEM)
}

type connectorEnrollmentRPC interface {
	EnrollConnector(
		context.Context,
		*controlv1.EnrollConnectorRequest,
		...grpc.CallOption,
	) (*controlv1.EnrollConnectorResponse, error)
}

type enrollmentRetryWait func(context.Context, time.Duration) error

// ValidateEnrollmentOptions rejects ambiguous identities, unsafe counters,
// non-origin endpoints, implicit TLS names, and missing trust files before any
// one-time token can be consumed.
func ValidateEnrollmentOptions(options EnrollmentOptions) error {
	for name, value := range map[string]string{
		"tenant ID":    options.TenantID,
		"host ID":      options.HostID,
		"Connector ID": options.ConnectorID,
		"request ID":   options.RequestID,
	} {
		if _, err := canonicalEnrollmentUUID(value); err != nil {
			return fmt.Errorf("vnext connector: %s must be a canonical UUIDv7", name)
		}
	}
	if !isPositiveJSONSafeCredentialInteger(options.Generation) ||
		!isPositiveJSONSafeCredentialInteger(options.SpecRevision) {
		return errors.New("vnext connector: generation and spec revision must be positive JSON-safe integers")
	}
	if err := validateEnrollmentEndpoint(options.EnrollmentURL, options.EnrollmentServerName, "enrollment"); err != nil {
		return err
	}
	if err := validateEnrollmentEndpoint(options.ControlURL, options.ControlServerName, "control"); err != nil {
		return err
	}
	for _, input := range []struct {
		name  string
		value string
	}{
		{"enrollment root CA file", options.EnrollmentRootCAFile},
		{"control server root CA file", options.ControlServerRootCAFile},
		{"connector issuer root CA file", options.ConnectorIssuerRootCAFile},
	} {
		name, value := input.name, input.value
		if value == "" || value != strings.TrimSpace(value) || len(value) > maxEnrollmentPathBytes || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("vnext connector: invalid %s", name)
		}
	}
	for _, pin := range []struct {
		name  string
		value string
	}{
		{"enrollment root CA SHA-256", options.EnrollmentRootCASHA256},
		{"control server root CA SHA-256", options.ControlServerRootCASHA256},
		{"connector issuer root CA SHA-256", options.ConnectorIssuerRootCASHA256},
	} {
		if _, err := decodeCanonicalSHA256(pin.value); err != nil {
			return fmt.Errorf("vnext connector: invalid %s", pin.name)
		}
	}
	return nil
}

// EnrollConnector performs one bounded, server-authenticated gRPC enrollment.
// Any retry reuses the same generated keys, signatures, and protobuf request.
func EnrollConnector(ctx context.Context, options EnrollmentOptions, token []byte) ([]byte, error) {
	if err := ValidateEnrollmentOptions(options); err != nil {
		return nil, err
	}
	if len(token) != enrollmentTokenBytes {
		return nil, errors.New("vnext connector: enrollment token must be exactly 32 raw bytes")
	}
	trustRoots, err := loadVerifiedEnrollmentTrustRoots(options)
	if err != nil {
		return nil, err
	}
	defer trustRoots.clear()

	connection, err := NewEnrollmentConnection(
		options.EnrollmentURL,
		options.EnrollmentServerName,
		trustRoots.enrollmentRootPEM,
	)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	return enrollConnectorWithRPC(
		ctx,
		options,
		token,
		trustRoots.controlServerRootPEM,
		trustRoots.connectorIssuerRootPEM,
		controlv1.NewConnectorEnrollmentClient(connection),
		rand.Reader,
		waitForEnrollmentRetry,
	)
}

func enrollConnectorWithRPC(
	ctx context.Context,
	options EnrollmentOptions,
	token []byte,
	controlServerRootPEM []byte,
	connectorIssuerRootPEM []byte,
	client connectorEnrollmentRPC,
	entropy io.Reader,
	retryWait enrollmentRetryWait,
) ([]byte, error) {
	if err := ValidateEnrollmentOptions(options); err != nil {
		return nil, err
	}
	if len(token) != enrollmentTokenBytes || client == nil || entropy == nil || retryWait == nil {
		return nil, errors.New("vnext connector: invalid enrollment input")
	}
	canonicalControlServerRoot, _, err := enrollmentTLSRoots(controlServerRootPEM)
	if err != nil {
		return nil, fmt.Errorf("vnext connector: invalid control server root CA")
	}
	defer clear(canonicalControlServerRoot)
	canonicalConnectorIssuerRoot, _, err := enrollmentTLSRoots(connectorIssuerRootPEM)
	if err != nil {
		return nil, fmt.Errorf("vnext connector: invalid connector issuer root CA")
	}
	defer clear(canonicalConnectorIssuerRoot)
	request, controlPrivate, refreshPrivate, _, _, requestDigest, err := buildEnrollmentRequest(options, token, entropy)
	if err != nil {
		return nil, err
	}
	defer clear(controlPrivate)
	defer clear(refreshPrivate)
	defer clear(request.EnrollmentToken)

	response, err := callEnrollmentWithRetry(ctx, client, request, retryWait)
	if err != nil {
		return nil, err
	}
	credential, err := buildEnrollmentCredentialDocument(
		options,
		request,
		requestDigest,
		controlPrivate,
		canonicalControlServerRoot,
		canonicalConnectorIssuerRoot,
		response,
	)
	if err != nil {
		return nil, fmt.Errorf("vnext connector: %w: unusable enrollment response", ErrEnrollmentOutcomeUnknown)
	}
	return credential, nil
}

func buildEnrollmentRequest(
	options EnrollmentOptions,
	token []byte,
	entropy io.Reader,
) (
	request *controlv1.EnrollConnectorRequest,
	controlPrivate ed25519.PrivateKey,
	refreshPrivate ed25519.PrivateKey,
	tokenDigest []byte,
	signingBytes []byte,
	requestDigest []byte,
	err error,
) {
	if err = ValidateEnrollmentOptions(options); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if len(token) != enrollmentTokenBytes {
		return nil, nil, nil, nil, nil, nil, errors.New("vnext connector: enrollment token must be exactly 32 raw bytes")
	}
	controlPublic, controlPrivate, err := ed25519.GenerateKey(entropy)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, errors.New("vnext connector: generate control key")
	}
	refreshPublic, refreshPrivate, err := ed25519.GenerateKey(entropy)
	if err != nil {
		clear(controlPrivate)
		return nil, nil, nil, nil, nil, nil, errors.New("vnext connector: generate refresh key")
	}
	if subtle.ConstantTimeCompare(controlPublic, refreshPublic) == 1 {
		clear(controlPrivate)
		clear(refreshPrivate)
		return nil, nil, nil, nil, nil, nil, errors.New("vnext connector: generated enrollment keys are not distinct")
	}
	tokenDigest = enrollmentDomainDigest(enrollmentTokenDigestDomain, token)
	signingBytes, err = enrollmentSigningBytes(options, tokenDigest, controlPublic, refreshPublic)
	if err != nil {
		clear(controlPrivate)
		clear(refreshPrivate)
		return nil, nil, nil, nil, nil, nil, err
	}
	request = &controlv1.EnrollConnectorRequest{
		EnrollmentToken:     append([]byte(nil), token...),
		EnrollmentRequestId: options.RequestID,
		TenantId:            options.TenantID,
		HostId:              options.HostID,
		ConnectorId:         options.ConnectorID,
		ConnectorGeneration: options.Generation,
		SpecRevision:        options.SpecRevision,
		ControlPublicKey:    append([]byte(nil), controlPublic...),
		RefreshPublicKey:    append([]byte(nil), refreshPublic...),
		ControlSignature:    ed25519.Sign(controlPrivate, signingBytes),
		RefreshSignature:    ed25519.Sign(refreshPrivate, signingBytes),
	}
	requestDigest = enrollmentDomainDigest(
		enrollmentRequestDigestDomain,
		signingBytes,
		request.ControlSignature,
		request.RefreshSignature,
	)
	return request, controlPrivate, refreshPrivate, tokenDigest, signingBytes, requestDigest, nil
}

func callEnrollmentWithRetry(
	ctx context.Context,
	client connectorEnrollmentRPC,
	request *controlv1.EnrollConnectorRequest,
	retryWait enrollmentRetryWait,
) (*controlv1.EnrollConnectorResponse, error) {
	wait := enrollmentInitialRetryWait
	var lastError error
	for attempt := 0; attempt < enrollmentMaximumAttempts; attempt++ {
		attemptContext, cancel := context.WithTimeout(ctx, enrollmentAttemptTimeout)
		response, err := client.EnrollConnector(
			attemptContext,
			request,
			grpc.MaxCallRecvMsgSize(maxAgentControlMessageBytes),
			grpc.MaxCallSendMsgSize(maxAgentControlMessageBytes),
		)
		cancel()
		if err == nil {
			return response, nil
		}
		lastError = err
		code := status.Code(err)
		if grpcStatusIsPermanent(err) {
			return nil, fmt.Errorf("vnext connector: enrollment RPC rejected with status %s", code)
		}
		if attempt+1 == enrollmentMaximumAttempts {
			break
		}
		if err := retryWait(ctx, wait); err != nil {
			return nil, fmt.Errorf("vnext connector: %w: enrollment retry interrupted", ErrEnrollmentOutcomeUnknown)
		}
		wait *= 2
	}
	return nil, fmt.Errorf("vnext connector: %w after bounded retries: %v", ErrEnrollmentOutcomeUnknown, status.Code(lastError))
}

func buildEnrollmentCredentialDocument(
	options EnrollmentOptions,
	request *controlv1.EnrollConnectorRequest,
	requestDigest []byte,
	controlPrivate ed25519.PrivateKey,
	controlServerRootPEM []byte,
	connectorIssuerRootPEM []byte,
	response *controlv1.EnrollConnectorResponse,
) ([]byte, error) {
	if response == nil || response.Credential == nil {
		return nil, errors.New("vnext connector: enrollment response omitted credential")
	}
	if len(response.ProtoReflect().GetUnknown()) != 0 || len(response.Credential.ProtoReflect().GetUnknown()) != 0 {
		return nil, errors.New("vnext connector: enrollment response contains unknown fields")
	}
	if len(response.RequestDigest) != sha256.Size || subtle.ConstantTimeCompare(response.RequestDigest, requestDigest) != 1 {
		return nil, errors.New("vnext connector: enrollment request digest mismatch")
	}
	credential := response.Credential
	if _, err := canonicalEnrollmentUUID(credential.CredentialId); err != nil ||
		!isPositiveJSONSafeCredentialInteger(credential.CredentialRevision) ||
		credential.CredentialRevision != options.SpecRevision ||
		len(credential.CertificateChainDer) == 0 || len(credential.CertificateChainDer) > maxControlCertificateChain ||
		len(credential.LeafFingerprint) != sha256.Size ||
		credential.ValidFromMillis > maxJSONSafeCredentialInteger ||
		credential.ValidUntilMillis > maxJSONSafeCredentialInteger ||
		credential.ValidUntilMillis <= credential.ValidFromMillis {
		return nil, errors.New("vnext connector: invalid enrollment credential metadata")
	}
	chainPEM := make([]byte, 0, 4096)
	var leaf *x509.Certificate
	for index, der := range credential.CertificateChainDer {
		if len(der) == 0 || len(der) > maxControlCertificateBytes {
			return nil, errors.New("vnext connector: invalid enrollment certificate chain")
		}
		certificate, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, errors.New("vnext connector: invalid enrollment certificate chain")
		}
		if index == 0 {
			leaf = certificate
		}
		chainPEM = append(chainPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	defer clear(chainPEM)
	// RFC 5280 certificate validity is second-precision. The server commits its
	// millisecond policy window in the result digest and rcgen truncates that
	// same positive value when encoding the leaf, so compare at the shared
	// precision without discarding the authenticated sub-second policy value.
	if leaf == nil || leaf.NotBefore.Unix() != int64(credential.ValidFromMillis/1_000) ||
		leaf.NotAfter.Unix() != int64(credential.ValidUntilMillis/1_000) {
		return nil, errors.New("vnext connector: certificate validity does not match enrollment result")
	}
	leafFingerprint := sha256.Sum256(credential.CertificateChainDer[0])
	if subtle.ConstantTimeCompare(credential.LeafFingerprint, leafFingerprint[:]) != 1 {
		return nil, errors.New("vnext connector: enrollment leaf fingerprint mismatch")
	}
	expectedResultDigest := enrollmentCredentialResultDigest(options, request, credential)
	if len(response.ResultDigest) != sha256.Size || len(expectedResultDigest) != sha256.Size ||
		subtle.ConstantTimeCompare(response.ResultDigest, expectedResultDigest) != 1 {
		return nil, errors.New("vnext connector: enrollment result digest mismatch")
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(controlPrivate)
	if err != nil {
		return nil, errors.New("vnext connector: marshal control private key")
	}
	defer clear(privateKeyDER)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	defer clear(privateKeyPEM)
	wire := controlCredentialJSONV2{
		SchemaVersion:            ControlCredentialSchemaVersionV2,
		TenantID:                 options.TenantID,
		ConnectorID:              options.ConnectorID,
		Generation:               options.Generation,
		CredentialRevision:       credential.CredentialRevision,
		ServerName:               options.ControlServerName,
		ServerRootCAPEM:          string(controlServerRootPEM),
		ConnectorIssuerRootCAPEM: string(connectorIssuerRootPEM),
		CertificateChainPEM:      string(chainPEM),
		PrivateKeyPEM:            string(privateKeyPEM),
		LeafFingerprintSHA256:    hex.EncodeToString(credential.LeafFingerprint),
	}
	encoded, err := json.Marshal(wire)
	wire.ServerRootCAPEM = ""
	wire.ConnectorIssuerRootCAPEM = ""
	wire.CertificateChainPEM = ""
	wire.PrivateKeyPEM = ""
	if err != nil {
		return nil, errors.New("vnext connector: encode control credential")
	}
	if len(encoded) == 0 || len(encoded) > MaxControlCredentialBytes {
		clear(encoded)
		return nil, ErrControlCredentialTooLarge
	}
	loaded, err := validateControlCredentialDocument(
		encoded,
		options.TenantID,
		options.ConnectorID,
		options.Generation,
		options.SpecRevision,
		options.ControlURL,
	)
	if err != nil {
		clear(encoded)
		return nil, fmt.Errorf("vnext connector: generated credential failed supervisor validation: %w", err)
	}
	if loaded.SchemaVersion != ControlCredentialSchemaVersionV2 ||
		loaded.CredentialRevision != options.SpecRevision ||
		loaded.ServerName != options.ControlServerName {
		clear(encoded)
		return nil, errors.New("vnext connector: generated credential metadata mismatch")
	}
	return encoded, nil
}

func enrollmentSigningBytes(
	options EnrollmentOptions,
	tokenDigest []byte,
	controlPublic []byte,
	refreshPublic []byte,
) ([]byte, error) {
	tenantID, err := canonicalEnrollmentUUID(options.TenantID)
	if err != nil {
		return nil, err
	}
	hostID, err := canonicalEnrollmentUUID(options.HostID)
	if err != nil {
		return nil, err
	}
	connectorID, err := canonicalEnrollmentUUID(options.ConnectorID)
	if err != nil {
		return nil, err
	}
	requestID, err := canonicalEnrollmentUUID(options.RequestID)
	if err != nil {
		return nil, err
	}
	if len(tokenDigest) != sha256.Size || len(controlPublic) != ed25519.PublicKeySize || len(refreshPublic) != ed25519.PublicKeySize {
		return nil, errors.New("vnext connector: invalid enrollment transcript key material")
	}
	transcript := make([]byte, 0, 320)
	transcript = appendEnrollmentLP(transcript, []byte(enrollmentProofDomain))
	for _, part := range [][]byte{
		tenantID,
		hostID,
		connectorID,
		enrollmentUint64(options.Generation),
		enrollmentUint64(options.SpecRevision),
		requestID,
		tokenDigest,
		controlPublic,
		refreshPublic,
	} {
		transcript = appendEnrollmentLP(transcript, part)
	}
	return transcript, nil
}

func enrollmentRequestDigest(request *controlv1.EnrollConnectorRequest) []byte {
	if request == nil {
		return nil
	}
	options := EnrollmentOptions{
		TenantID:     request.TenantId,
		HostID:       request.HostId,
		ConnectorID:  request.ConnectorId,
		Generation:   request.ConnectorGeneration,
		SpecRevision: request.SpecRevision,
		RequestID:    request.EnrollmentRequestId,
	}
	tokenDigest := enrollmentDomainDigest(enrollmentTokenDigestDomain, request.EnrollmentToken)
	signingBytes, err := enrollmentSigningBytes(options, tokenDigest, request.ControlPublicKey, request.RefreshPublicKey)
	if err != nil {
		return nil
	}
	return enrollmentDomainDigest(
		enrollmentRequestDigestDomain,
		signingBytes,
		request.ControlSignature,
		request.RefreshSignature,
	)
}

func enrollmentCredentialResultDigest(
	options EnrollmentOptions,
	request *controlv1.EnrollConnectorRequest,
	credential *controlv1.ConnectorCredential,
) []byte {
	if request == nil || credential == nil {
		return nil
	}
	credentialID, err := canonicalEnrollmentUUID(credential.CredentialId)
	if err != nil {
		return nil
	}
	tenantID, err := canonicalEnrollmentUUID(options.TenantID)
	if err != nil {
		return nil
	}
	connectorID, err := canonicalEnrollmentUUID(options.ConnectorID)
	if err != nil {
		return nil
	}
	parts := [][]byte{
		credentialID,
		tenantID,
		connectorID,
		enrollmentUint64(options.Generation),
		enrollmentUint64(credential.CredentialRevision),
		request.ControlPublicKey,
		request.RefreshPublicKey,
		credential.LeafFingerprint,
		enrollmentUint64(uint64(len(credential.CertificateChainDer))),
	}
	parts = append(parts, credential.CertificateChainDer...)
	// Rust commits signed i64 millisecond values. Valid protobuf values are
	// constrained to the positive JSON-safe range, so their big-endian bytes
	// are identical to these uint64 encodings.
	parts = append(parts, enrollmentUint64(credential.ValidFromMillis), enrollmentUint64(credential.ValidUntilMillis))
	return enrollmentDomainDigest(enrollmentResultDigestDomain, parts...)
}

func enrollmentDomainDigest(domain string, parts ...[]byte) []byte {
	hash := sha256.New()
	_, _ = hash.Write(enrollmentUint64(uint64(len(domain))))
	_, _ = hash.Write([]byte(domain))
	for _, part := range parts {
		_, _ = hash.Write(enrollmentUint64(uint64(len(part))))
		_, _ = hash.Write(part)
	}
	return hash.Sum(nil)
}

func appendEnrollmentLP(destination []byte, value []byte) []byte {
	destination = append(destination, enrollmentUint64(uint64(len(value)))...)
	return append(destination, value...)
}

func enrollmentUint64(value uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, value)
	return encoded
}

func canonicalEnrollmentUUID(value string) ([]byte, error) {
	if !controlCredentialUUIDv7Pattern.MatchString(value) {
		return nil, errors.New("noncanonical UUIDv7")
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 {
		return nil, errors.New("invalid UUIDv7")
	}
	return append([]byte(nil), parsed[:]...), nil
}

func validateEnrollmentEndpoint(value, serverName, label string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Fragment != "" || parsed.RawQuery != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.Hostname() == "" {
		return fmt.Errorf("vnext connector: %s URL must be an origin HTTPS URL", label)
	}
	hostname := parsed.Hostname()
	if serverName == "" || serverName != strings.TrimSpace(serverName) || serverName != strings.ToLower(serverName) ||
		hostname != strings.ToLower(hostname) || serverName != hostname {
		return fmt.Errorf("vnext connector: %s server name must exactly match the lowercase URL host", label)
	}
	return nil
}

func loadVerifiedEnrollmentTrustRoots(options EnrollmentOptions) (*verifiedEnrollmentTrustRoots, error) {
	enrollmentRootPEM, err := readVerifiedEnrollmentCAFile(options.EnrollmentRootCAFile, options.EnrollmentRootCASHA256)
	if err != nil {
		return nil, fmt.Errorf("vnext connector: read enrollment root CA: %w", err)
	}
	controlServerRootPEM, err := readVerifiedEnrollmentCAFile(options.ControlServerRootCAFile, options.ControlServerRootCASHA256)
	if err != nil {
		clear(enrollmentRootPEM)
		return nil, fmt.Errorf("vnext connector: read control server root CA: %w", err)
	}
	connectorIssuerRootPEM, err := readVerifiedEnrollmentCAFile(options.ConnectorIssuerRootCAFile, options.ConnectorIssuerRootCASHA256)
	if err != nil {
		clear(enrollmentRootPEM)
		clear(controlServerRootPEM)
		return nil, fmt.Errorf("vnext connector: read connector issuer root CA: %w", err)
	}
	return &verifiedEnrollmentTrustRoots{
		enrollmentRootPEM:      enrollmentRootPEM,
		controlServerRootPEM:   controlServerRootPEM,
		connectorIssuerRootPEM: connectorIssuerRootPEM,
	}, nil
}

func readVerifiedEnrollmentCAFile(path, expectedSHA256 string) ([]byte, error) {
	expected, err := decodeCanonicalSHA256(expectedSHA256)
	if err != nil {
		return nil, errors.New("CA SHA-256 pin is not canonical lowercase hex")
	}
	contents, err := readEnrollmentCAFile(path)
	if err != nil {
		return nil, err
	}
	actual := sha256.Sum256(contents)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		clear(contents)
		return nil, errors.New("CA SHA-256 pin mismatch")
	}
	if _, _, err := enrollmentTLSRoots(contents); err != nil {
		clear(contents)
		return nil, errors.New("CA file does not contain a strict CA bundle")
	}
	return contents, nil
}

func readEnrollmentCAFile(path string) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() ||
		pathInfo.Size() <= 0 || pathInfo.Size() > maxEnrollmentCABytes {
		return nil, errors.New("CA path must be a bounded regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open CA file")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return nil, errors.New("CA file changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxEnrollmentCABytes+1))
	if err != nil || len(contents) == 0 || len(contents) > maxEnrollmentCABytes {
		clear(contents)
		return nil, errors.New("cannot read bounded CA file")
	}
	afterInfo, err := file.Stat()
	currentPathInfo, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !os.SameFile(openedInfo, afterInfo) ||
		afterInfo.Size() != int64(len(contents)) || !afterInfo.ModTime().Equal(openedInfo.ModTime()) ||
		currentPathInfo.Mode()&os.ModeSymlink != 0 || !currentPathInfo.Mode().IsRegular() ||
		!os.SameFile(currentPathInfo, afterInfo) {
		clear(contents)
		return nil, errors.New("CA file changed while reading")
	}
	return contents, nil
}

func enrollmentTLSRoots(contents []byte) ([]byte, *x509.CertPool, error) {
	if len(contents) == 0 || len(contents) > maxEnrollmentCABytes {
		return nil, nil, ErrInvalidControlCredential
	}
	certificates, err := parseStrictCertificatePEM(string(contents))
	if err != nil {
		return nil, nil, err
	}
	canonical := make([]byte, 0, len(contents))
	pool := x509.NewCertPool()
	for _, certificate := range certificates {
		if !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
			clear(canonical)
			return nil, nil, ErrInvalidControlCredential
		}
		canonical = append(canonical, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})...)
		pool.AddCert(certificate)
	}
	return canonical, pool, nil
}

func waitForEnrollmentRetry(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
