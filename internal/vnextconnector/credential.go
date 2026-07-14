package vnextconnector

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ControlCredentialSchemaVersion uint16 = 1
	MaxControlCredentialBytes             = 64 * 1024
	maxJSONSafeCredentialInteger   uint64 = 9_007_199_254_740_991
	maxControlCertificateBytes            = 16 * 1024
	maxControlCertificateChain            = 4
)

var (
	ErrInvalidControlCredential  = errors.New("invalid control credential")
	ErrUnsafeControlCredential   = errors.New("unsafe control credential file")
	ErrControlCredentialTooLarge = errors.New("control credential exceeds 64 KiB")
)

var controlCredentialUUIDv7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var (
	oidCommonName             = asn1.ObjectIdentifier{2, 5, 4, 3}
	oidSubjectAlternativeName = asn1.ObjectIdentifier{2, 5, 29, 17}
)

// ControlCredential is the validated, non-secret metadata and private TLS
// template for one supervisor-managed Connector generation. Raw PEM is never
// retained or exposed.
type ControlCredential struct {
	SchemaVersion      uint16
	TenantID           string
	ConnectorID        string
	Generation         uint64
	CredentialRevision uint64
	ServerName         string

	tlsConfig *tls.Config
}

// TLSConfig returns a fresh TLS 1.3-only HTTP/2 client configuration. Mutable
// slices and the root pool are cloned so a caller cannot alter future control
// connections through a previously returned value.
func (credential *ControlCredential) TLSConfig() *tls.Config {
	if credential == nil || credential.tlsConfig == nil {
		return nil
	}
	return cloneControlTLSConfig(credential.tlsConfig)
}

func (credential *ControlCredential) String() string {
	if credential == nil {
		return "ControlCredential(<nil>)"
	}
	return fmt.Sprintf(
		"ControlCredential{schema_version:%d tenant_id:%s connector_id:%s generation:%d credential_revision:%d server_name:%s tls:[redacted]}",
		credential.SchemaVersion,
		credential.TenantID,
		credential.ConnectorID,
		credential.Generation,
		credential.CredentialRevision,
		credential.ServerName,
	)
}

func (credential *ControlCredential) GoString() string {
	return credential.String()
}

type controlCredentialJSON struct {
	SchemaVersion         uint16 `json:"schema_version"`
	TenantID              string `json:"tenant_id"`
	ConnectorID           string `json:"connector_id"`
	Generation            uint64 `json:"generation"`
	CredentialRevision    uint64 `json:"credential_revision"`
	ServerName            string `json:"server_name"`
	RootCAPEM             string `json:"root_ca_pem"`
	CertificateChainPEM   string `json:"certificate_chain_pem"`
	PrivateKeyPEM         string `json:"private_key_pem"`
	LeafFingerprintSHA256 string `json:"leaf_fingerprint_sha256"`
}

// LoadControlCredential reads and authenticates one bounded credential file
// against the expected process identity, generation, and control node.
func LoadControlCredential(
	path string,
	expectedTenantID string,
	expectedConnectorID string,
	expectedGeneration uint64,
	nodeURL string,
) (*ControlCredential, error) {
	if !controlCredentialUUIDv7Pattern.MatchString(expectedTenantID) ||
		!controlCredentialUUIDv7Pattern.MatchString(expectedConnectorID) ||
		!isPositiveJSONSafeCredentialInteger(expectedGeneration) {
		return nil, invalidControlCredential("expected identity or generation")
	}
	if _, err := validatedControlNodeServerName(nodeURL); err != nil {
		return nil, err
	}
	contents, err := readControlCredentialFile(path)
	if err != nil {
		return nil, err
	}
	defer clear(contents)
	return validateControlCredentialDocument(
		contents,
		expectedTenantID,
		expectedConnectorID,
		expectedGeneration,
		nodeURL,
	)
}

// validateControlCredentialDocument is the shared in-memory half of the
// supervisor loader. Enrollment calls it before emitting generated credential
// bytes, while LoadControlCredential retains the filesystem safety boundary.
func validateControlCredentialDocument(
	contents []byte,
	expectedTenantID string,
	expectedConnectorID string,
	expectedGeneration uint64,
	nodeURL string,
) (*ControlCredential, error) {
	if !controlCredentialUUIDv7Pattern.MatchString(expectedTenantID) ||
		!controlCredentialUUIDv7Pattern.MatchString(expectedConnectorID) ||
		!isPositiveJSONSafeCredentialInteger(expectedGeneration) {
		return nil, invalidControlCredential("expected identity or generation")
	}
	nodeServerName, err := validatedControlNodeServerName(nodeURL)
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 || len(contents) > MaxControlCredentialBytes {
		return nil, ErrControlCredentialTooLarge
	}
	if !utf8.Valid(contents) {
		return nil, invalidControlCredential("JSON is not valid UTF-8")
	}
	if err := validateUniqueJSONFields(contents); err != nil {
		return nil, invalidControlCredential("duplicate or malformed JSON field")
	}

	var wire controlCredentialJSON
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, invalidControlCredential("JSON document")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	defer func() {
		wire.RootCAPEM = ""
		wire.CertificateChainPEM = ""
		wire.PrivateKeyPEM = ""
	}()

	if wire.SchemaVersion != ControlCredentialSchemaVersion ||
		!controlCredentialUUIDv7Pattern.MatchString(wire.TenantID) ||
		!controlCredentialUUIDv7Pattern.MatchString(wire.ConnectorID) ||
		wire.TenantID != expectedTenantID ||
		wire.ConnectorID != expectedConnectorID ||
		wire.Generation != expectedGeneration ||
		!isPositiveJSONSafeCredentialInteger(wire.Generation) ||
		!isPositiveJSONSafeCredentialInteger(wire.CredentialRevision) {
		return nil, invalidControlCredential("schema, identity, or fence")
	}
	if wire.ServerName != nodeServerName {
		return nil, invalidControlCredential("server_name does not match control node")
	}

	rootCertificates, err := parseStrictCertificatePEM(wire.RootCAPEM)
	if err != nil {
		return nil, invalidControlCredential("root_ca_pem")
	}
	chainCertificates, err := parseStrictCertificatePEM(wire.CertificateChainPEM)
	if err != nil {
		return nil, invalidControlCredential("certificate_chain_pem")
	}
	privateKeyDER, err := parseStrictPKCS8PrivateKeyPEM(wire.PrivateKeyPEM)
	if err != nil {
		return nil, invalidControlCredential("private_key_pem")
	}
	defer clear(privateKeyDER)

	leaf := chainCertificates[0]
	expectedIdentity := "spiffe://dirextalk.internal/v1/tenants/" + wire.TenantID +
		"/connectors/" + wire.ConnectorID
	if err := validateControlLeafStructure(leaf, expectedIdentity); err != nil {
		return nil, err
	}
	if err := validateControlPrivateKey(privateKeyDER, leaf); err != nil {
		return nil, err
	}

	roots := x509.NewCertPool()
	for _, certificate := range rootCertificates {
		if !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, invalidControlCredential("root_ca_pem contains a non-CA certificate")
		}
		roots.AddCert(certificate)
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range chainCertificates[1:] {
		if !certificate.IsCA || !certificate.BasicConstraintsValid {
			return nil, invalidControlCredential("certificate chain intermediate")
		}
		intermediates.AddCert(certificate)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || leaf.NotAfter.Sub(leaf.NotBefore) > 24*time.Hour {
		return nil, invalidControlCredential("certificate validity")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		CurrentTime:   now,
	}); err != nil {
		return nil, invalidControlCredential("certificate trust chain")
	}

	fingerprint, err := decodeCanonicalSHA256(wire.LeafFingerprintSHA256)
	if err != nil {
		return nil, invalidControlCredential("leaf_fingerprint_sha256")
	}
	actualFingerprint := sha256.Sum256(leaf.Raw)
	if subtle.ConstantTimeCompare(fingerprint, actualFingerprint[:]) != 1 {
		return nil, invalidControlCredential("leaf fingerprint mismatch")
	}

	chainPEM := []byte(wire.CertificateChainPEM)
	privateKeyPEM := []byte(wire.PrivateKeyPEM)
	keyPair, err := tls.X509KeyPair(chainPEM, privateKeyPEM)
	clear(chainPEM)
	clear(privateKeyPEM)
	if err != nil {
		return nil, invalidControlCredential("TLS key pair")
	}
	keyPair.Leaf = nil
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		ServerName:   wire.ServerName,
		RootCAs:      roots,
		Certificates: []tls.Certificate{keyPair},
		NextProtos:   []string{"h2"},
	}

	return &ControlCredential{
		SchemaVersion:      wire.SchemaVersion,
		TenantID:           wire.TenantID,
		ConnectorID:        wire.ConnectorID,
		Generation:         wire.Generation,
		CredentialRevision: wire.CredentialRevision,
		ServerName:         wire.ServerName,
		tlsConfig:          tlsConfig,
	}, nil
}

func readControlCredentialFile(path string) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot inspect file", ErrUnsafeControlCredential)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || pathInfo.Size() <= 0 {
		return nil, fmt.Errorf("%w: file must be a non-empty regular non-symlink", ErrUnsafeControlCredential)
	}
	if pathInfo.Size() > MaxControlCredentialBytes {
		return nil, ErrControlCredentialTooLarge
	}
	file, err := openStateFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open file", ErrUnsafeControlCredential)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) || openedInfo.Size() != pathInfo.Size() {
		return nil, fmt.Errorf("%w: file changed while opening", ErrUnsafeControlCredential)
	}
	if err := validateControlCredentialFileSecurity(file, openedInfo); err != nil {
		return nil, err
	}
	currentPathInfo, err := os.Lstat(path)
	if err != nil || currentPathInfo.Mode()&os.ModeSymlink != 0 || !currentPathInfo.Mode().IsRegular() ||
		!os.SameFile(currentPathInfo, openedInfo) {
		return nil, fmt.Errorf("%w: path changed while opening", ErrUnsafeControlCredential)
	}

	contents, err := io.ReadAll(io.LimitReader(file, MaxControlCredentialBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read file", ErrUnsafeControlCredential)
	}
	if len(contents) > MaxControlCredentialBytes {
		clear(contents)
		return nil, ErrControlCredentialTooLarge
	}
	afterInfo, err := file.Stat()
	if err != nil || !os.SameFile(openedInfo, afterInfo) || afterInfo.Size() != int64(len(contents)) ||
		!afterInfo.ModTime().Equal(openedInfo.ModTime()) {
		clear(contents)
		return nil, fmt.Errorf("%w: file changed while reading", ErrUnsafeControlCredential)
	}
	currentPathInfo, err = os.Lstat(path)
	if err != nil || currentPathInfo.Mode()&os.ModeSymlink != 0 || !currentPathInfo.Mode().IsRegular() ||
		!os.SameFile(currentPathInfo, afterInfo) {
		clear(contents)
		return nil, fmt.Errorf("%w: path changed while reading", ErrUnsafeControlCredential)
	}
	return contents, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalidControlCredential("trailing JSON content")
	}
	return nil
}

func parseStrictCertificatePEM(value string) ([]*x509.Certificate, error) {
	remaining := []byte(value)
	defer clear(remaining)
	certificates := make([]*x509.Certificate, 0, 1)
	for len(bytes.TrimSpace(remaining)) > 0 {
		remaining = bytes.TrimSpace(remaining)
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, ErrInvalidControlCredential
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 ||
			len(block.Bytes) == 0 || len(block.Bytes) > maxControlCertificateBytes {
			return nil, ErrInvalidControlCredential
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, ErrInvalidControlCredential
		}
		certificates = append(certificates, certificate)
		if len(certificates) > maxControlCertificateChain {
			return nil, ErrInvalidControlCredential
		}
		remaining = rest
	}
	if len(certificates) == 0 {
		return nil, ErrInvalidControlCredential
	}
	return certificates, nil
}

func parseStrictPKCS8PrivateKeyPEM(value string) ([]byte, error) {
	contents := []byte(value)
	defer clear(contents)
	trimmed := bytes.TrimSpace(contents)
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN PRIVATE KEY-----")) {
		return nil, ErrInvalidControlCredential
	}
	block, rest := pem.Decode(trimmed)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(block.Bytes) == 0 ||
		len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrInvalidControlCredential
	}
	return append([]byte(nil), block.Bytes...), nil
}

func validateControlLeafStructure(leaf *x509.Certificate, expectedIdentity string) error {
	if leaf == nil || leaf.IsCA || !leaf.BasicConstraintsValid || leaf.PublicKeyAlgorithm != x509.Ed25519 ||
		leaf.SignatureAlgorithm != x509.PureEd25519 ||
		leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		return invalidControlCredential("Ed25519 client leaf")
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || len(leaf.UnknownExtKeyUsage) != 0 {
		return invalidControlCredential("clientAuth-only EKU")
	}
	for _, attribute := range leaf.Subject.Names {
		if attribute.Type.Equal(oidCommonName) {
			return invalidControlCredential("subject common name must be absent")
		}
	}
	if len(leaf.DNSNames) != 0 || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 ||
		len(leaf.URIs) != 1 || leaf.URIs[0].String() != expectedIdentity {
		return invalidControlCredential("single exact Connector URI SAN")
	}
	if err := validateExactURISANExtension(leaf, expectedIdentity); err != nil {
		return err
	}
	return nil
}

func validateExactURISANExtension(leaf *x509.Certificate, expectedIdentity string) error {
	var extensionValue []byte
	for _, extension := range leaf.Extensions {
		if extension.Id.Equal(oidSubjectAlternativeName) {
			if extensionValue != nil {
				return invalidControlCredential("duplicate SAN extension")
			}
			extensionValue = extension.Value
		}
	}
	if extensionValue == nil {
		return invalidControlCredential("missing SAN extension")
	}
	var sequence asn1.RawValue
	rest, err := asn1.Unmarshal(extensionValue, &sequence)
	if err != nil || len(rest) != 0 || sequence.Class != asn1.ClassUniversal ||
		sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return invalidControlCredential("SAN encoding")
	}
	var generalName asn1.RawValue
	rest, err = asn1.Unmarshal(sequence.Bytes, &generalName)
	if err != nil || len(rest) != 0 || generalName.Class != asn1.ClassContextSpecific ||
		generalName.Tag != 6 || generalName.IsCompound || string(generalName.Bytes) != expectedIdentity {
		return invalidControlCredential("single exact Connector URI SAN")
	}
	return nil
}

func validateControlPrivateKey(privateKeyDER []byte, leaf *x509.Certificate) error {
	parsed, err := x509.ParsePKCS8PrivateKey(privateKeyDER)
	if err != nil {
		return invalidControlCredential("PKCS#8 private key")
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return invalidControlCredential("Ed25519 private key")
	}
	publicKey, ok := leaf.PublicKey.(ed25519.PublicKey)
	if !ok || !privateKey.Public().(ed25519.PublicKey).Equal(publicKey) {
		return invalidControlCredential("private key does not match leaf")
	}
	return nil
}

func decodeCanonicalSHA256(value string) ([]byte, error) {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return nil, ErrInvalidControlCredential
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, ErrInvalidControlCredential
	}
	return decoded, nil
}

func validatedControlNodeServerName(value string) (string, error) {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Fragment != "" || parsed.RawQuery != "" || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.Hostname() == "" {
		return "", invalidControlCredential("node URL must be absolute HTTPS")
	}
	return parsed.Hostname(), nil
}

func cloneControlTLSConfig(template *tls.Config) *tls.Config {
	clone := template.Clone()
	clone.NextProtos = append([]string(nil), template.NextProtos...)
	if template.RootCAs != nil {
		clone.RootCAs = template.RootCAs.Clone()
	}
	clone.Certificates = make([]tls.Certificate, len(template.Certificates))
	for i := range template.Certificates {
		clone.Certificates[i] = template.Certificates[i]
		clone.Certificates[i].Certificate = make([][]byte, len(template.Certificates[i].Certificate))
		for j := range template.Certificates[i].Certificate {
			clone.Certificates[i].Certificate[j] = append([]byte(nil), template.Certificates[i].Certificate[j]...)
		}
		clone.Certificates[i].OCSPStaple = append([]byte(nil), template.Certificates[i].OCSPStaple...)
		clone.Certificates[i].SignedCertificateTimestamps = cloneByteSlices(template.Certificates[i].SignedCertificateTimestamps)
		if privateKey, ok := template.Certificates[i].PrivateKey.(ed25519.PrivateKey); ok {
			clone.Certificates[i].PrivateKey = append(ed25519.PrivateKey(nil), privateKey...)
		}
	}
	return clone
}

func cloneByteSlices(values [][]byte) [][]byte {
	cloned := make([][]byte, len(values))
	for i := range values {
		cloned[i] = append([]byte(nil), values[i]...)
	}
	return cloned
}

func isPositiveJSONSafeCredentialInteger(value uint64) bool {
	return value >= 1 && value <= maxJSONSafeCredentialInteger
}

func invalidControlCredential(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidControlCredential, reason)
}
