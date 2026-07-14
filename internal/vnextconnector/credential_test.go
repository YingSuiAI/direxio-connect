package vnextconnector

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testNodeURL = "https://control.example.com:8443"

const testSpecRevision uint64 = 11

func TestLoadControlCredentialBuildsStrictTLSConfig(t *testing.T) {
	fixture := writeControlCredentialFixtureForSchema(
		t,
		ControlCredentialSchemaVersionV2,
		credentialCertificateOptions{},
		nil,
		"",
	)

	credential, err := LoadControlCredential(
		fixture.path,
		testTenantID,
		testConnectorID,
		7,
		testSpecRevision,
		testNodeURL,
	)
	if err != nil {
		t.Fatalf("LoadControlCredential: %v", err)
	}
	if credential.SchemaVersion != ControlCredentialSchemaVersionV2 || credential.TenantID != testTenantID || credential.ConnectorID != testConnectorID {
		t.Fatalf("credential identity = %+v", credential)
	}
	if credential.Generation != 7 || credential.CredentialRevision != testSpecRevision {
		t.Fatalf("credential fence = generation %d revision %d", credential.Generation, credential.CredentialRevision)
	}

	tlsConfig := credential.TLSConfig()
	if tlsConfig == nil {
		t.Fatal("TLSConfig returned nil")
	}
	if tlsConfig.MinVersion != tls.VersionTLS13 || tlsConfig.MaxVersion != tls.VersionTLS13 {
		t.Fatalf("TLS versions = %x..%x, want TLS 1.3 only", tlsConfig.MinVersion, tlsConfig.MaxVersion)
	}
	if tlsConfig.ServerName != "control.example.com" || tlsConfig.InsecureSkipVerify {
		t.Fatalf("TLS server verification = name %q insecure=%v", tlsConfig.ServerName, tlsConfig.InsecureSkipVerify)
	}
	if !reflect.DeepEqual(tlsConfig.NextProtos, []string{"h2"}) {
		t.Fatalf("TLS ALPN = %v, want [h2]", tlsConfig.NextProtos)
	}
	if tlsConfig.RootCAs == nil || len(tlsConfig.Certificates) != 1 {
		t.Fatalf("TLS trust/client certificates not configured")
	}
	if !certificatePoolContainsSubject(tlsConfig.RootCAs, fixture.serverRoot) {
		t.Fatal("TLS RootCAs does not contain the independent control server root")
	}
	if certificatePoolContainsSubject(tlsConfig.RootCAs, fixture.connectorIssuerRoot) {
		t.Fatal("TLS RootCAs incorrectly trusts the connector certificate issuer")
	}

	// Callers receive a clone and cannot mutate the stored template used by a
	// later control connection.
	tlsConfig.NextProtos[0] = "http/1.1"
	if got := credential.TLSConfig().NextProtos; !reflect.DeepEqual(got, []string{"h2"}) {
		t.Fatalf("TLSConfig was not cloned: %v", got)
	}
	privateKey, ok := tlsConfig.Certificates[0].PrivateKey.(ed25519.PrivateKey)
	if !ok || len(privateKey) == 0 {
		t.Fatal("TLS private key is not Ed25519")
	}
	privateKey[0] ^= 0xff
	freshPrivateKey := credential.TLSConfig().Certificates[0].PrivateKey.(ed25519.PrivateKey)
	if freshPrivateKey[0] == privateKey[0] {
		t.Fatal("TLSConfig private key shares mutable storage with the credential template")
	}

	formatted := fmt.Sprintf("%+v %#v", credential, credential)
	for _, secret := range []string{"PRIVATE KEY", "CERTIFICATE", "root_ca_pem", "server_root_ca_pem", "connector_issuer_root_ca_pem", "private_key_pem"} {
		if strings.Contains(formatted, secret) {
			t.Fatalf("formatted credential leaked %q: %s", secret, formatted)
		}
	}
}

func TestLoadControlCredentialRejectsStaleSchemaV2Revision(t *testing.T) {
	fixture := writeControlCredentialFixtureForSchema(
		t,
		ControlCredentialSchemaVersionV2,
		credentialCertificateOptions{},
		nil,
		"",
	)
	if _, err := LoadControlCredential(
		fixture.path,
		testTenantID,
		testConnectorID,
		7,
		testSpecRevision+1,
		testNodeURL,
	); err == nil {
		t.Fatal("LoadControlCredential accepted a schema-v2 credential from an older spec revision")
	}
	encoded, err := os.ReadFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateControlCredentialDocument(
		encoded,
		testTenantID,
		testConnectorID,
		7,
		testSpecRevision+1,
		testNodeURL,
	); err == nil {
		t.Fatal("in-memory schema-v2 validator accepted a credential from an older spec revision")
	}
}

func TestLoadControlCredentialSupportsLegacySchemaV1SingleRoot(t *testing.T) {
	fixture := writeControlCredentialFixtureForSchema(
		t,
		ControlCredentialSchemaVersionV1,
		credentialCertificateOptions{},
		nil,
		"",
	)

	credential, err := LoadLegacyControlCredentialForMigration(
		fixture.path,
		testTenantID,
		testConnectorID,
		7,
		testNodeURL,
	)
	if err != nil {
		t.Fatalf("LoadLegacyControlCredentialForMigration: %v", err)
	}
	if credential.SchemaVersion != ControlCredentialSchemaVersionV1 {
		t.Fatalf("schema version = %d, want legacy v1", credential.SchemaVersion)
	}
	if !certificatePoolContainsSubject(credential.TLSConfig().RootCAs, fixture.connectorIssuerRoot) {
		t.Fatal("legacy single root was not retained for v1 TLS compatibility")
	}
}

func TestLoadControlCredentialRejectsLegacySchemaV1ByDefault(t *testing.T) {
	fixture := writeControlCredentialFixtureForSchema(
		t,
		ControlCredentialSchemaVersionV1,
		credentialCertificateOptions{},
		nil,
		"",
	)
	if _, err := LoadControlCredential(
		fixture.path,
		testTenantID,
		testConnectorID,
		7,
		testSpecRevision,
		testNodeURL,
	); err == nil {
		t.Fatal("default schema-v2 loader accepted a legacy credential")
	}
	v2Fixture := writeControlCredentialFixtureForSchema(
		t,
		ControlCredentialSchemaVersionV2,
		credentialCertificateOptions{},
		nil,
		"",
	)
	if _, err := LoadLegacyControlCredentialForMigration(
		v2Fixture.path,
		testTenantID,
		testConnectorID,
		7,
		testNodeURL,
	); err == nil {
		t.Fatal("legacy migration loader accepted a schema-v2 credential")
	}
}

func TestLoadControlCredentialRejectsCrossSchemaTrustFields(t *testing.T) {
	tests := []struct {
		name   string
		schema uint16
		mutate func(map[string]any)
	}{
		{
			name:   "v2 contains legacy root",
			schema: ControlCredentialSchemaVersionV2,
			mutate: func(value map[string]any) { value["root_ca_pem"] = "unexpected" },
		},
		{
			name:   "v1 contains split roots",
			schema: ControlCredentialSchemaVersionV1,
			mutate: func(value map[string]any) { value["server_root_ca_pem"] = "unexpected" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := writeControlCredentialFixtureForSchema(t, test.schema, credentialCertificateOptions{}, test.mutate, "")
			var err error
			if test.schema == ControlCredentialSchemaVersionV1 {
				_, err = LoadLegacyControlCredentialForMigration(fixture.path, testTenantID, testConnectorID, 7, testNodeURL)
			} else {
				_, err = LoadControlCredential(fixture.path, testTenantID, testConnectorID, 7, testSpecRevision, testNodeURL)
			}
			if err == nil {
				t.Fatal("LoadControlCredential accepted fields from another credential schema")
			}
		})
	}
}

func TestLoadControlCredentialRejectsCaseInsensitiveTopLevelAliases(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name: "schema version alias",
			mutate: func(encoded []byte) []byte {
				return bytes.Replace(encoded, []byte(`"schema_version":2`), []byte(`"Schema_Version":2`), 1)
			},
		},
		{
			name: "server name alias",
			mutate: func(encoded []byte) []byte {
				return bytes.Replace(encoded, []byte(`"server_name"`), []byte(`"Server_Name"`), 1)
			},
		},
		{
			name: "server name exact and alias",
			mutate: func(encoded []byte) []byte {
				return bytes.Replace(
					encoded,
					[]byte(`"server_name":"control.example.com"`),
					[]byte(`"server_name":"control.example.com","Server_Name":"control.example.com"`),
					1,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := writeControlCredentialFixture(t, credentialCertificateOptions{}, nil, "")
			encoded, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			mutated := test.mutate(encoded)
			if bytes.Equal(mutated, encoded) {
				t.Fatal("test mutation did not change the credential document")
			}
			if err := os.WriteFile(fixture, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadControlCredential(fixture, testTenantID, testConnectorID, 7, testSpecRevision, testNodeURL); err == nil {
				t.Fatal("LoadControlCredential accepted a case-insensitive top-level field alias")
			}
		})
	}
}

func TestLoadControlCredentialRejectsUntrustedContent(t *testing.T) {
	tests := []struct {
		name    string
		options credentialCertificateOptions
		mutate  func(map[string]any)
		suffix  string
	}{
		{name: "unknown JSON field", mutate: func(value map[string]any) { value["unexpected"] = true }},
		{name: "trailing JSON value", suffix: "\n{}"},
		{name: "wrong generation", mutate: func(value map[string]any) { value["generation"] = 8 }},
		{name: "unsafe credential revision", mutate: func(value map[string]any) { value["credential_revision"] = uint64(9_007_199_254_740_992) }},
		{name: "node server mismatch", mutate: func(value map[string]any) { value["server_name"] = "other.example.com" }},
		{name: "fingerprint mismatch", mutate: func(value map[string]any) { value["leaf_fingerprint_sha256"] = strings.Repeat("0", 64) }},
		{name: "common name", options: credentialCertificateOptions{commonName: "connector"}},
		{name: "additional SAN", options: credentialCertificateOptions{dnsNames: []string{"connector.example.com"}}},
		{name: "server auth EKU", options: credentialCertificateOptions{extendedKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}},
		{name: "expired leaf", options: credentialCertificateOptions{expired: true}},
		{name: "mismatched private key", mutate: func(value map[string]any) {
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatalf("generate mismatched key: %v", err)
			}
			der, err := x509.MarshalPKCS8PrivateKey(key)
			if err != nil {
				t.Fatalf("marshal mismatched key: %v", err)
			}
			value["private_key_pem"] = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeControlCredentialFixture(t, test.options, test.mutate, test.suffix)
			if _, err := LoadControlCredential(path, testTenantID, testConnectorID, 7, testSpecRevision, testNodeURL); err == nil {
				t.Fatal("LoadControlCredential succeeded, want rejection")
			}
		})
	}

	duplicatePath := writeControlCredentialFixture(t, credentialCertificateOptions{}, nil, "")
	encoded, err := os.ReadFile(duplicatePath)
	if err != nil {
		t.Fatal(err)
	}
	encoded = bytes.Replace(encoded, []byte(`"tenant_id":`), []byte(`"tenant_id":"duplicate","tenant_id":`), 1)
	if err := os.WriteFile(duplicatePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadControlCredential(duplicatePath, testTenantID, testConnectorID, 7, testSpecRevision, testNodeURL); err == nil {
		t.Fatal("duplicate credential JSON field accepted")
	}
}

func TestLoadControlCredentialRejectsUnsafeFiles(t *testing.T) {
	oversize := filepath.Join(t.TempDir(), "oversize.credential")
	if err := os.WriteFile(oversize, make([]byte, 64*1024+1), 0o600); err != nil {
		t.Fatalf("write oversize credential: %v", err)
	}
	if _, err := LoadControlCredential(oversize, testTenantID, testConnectorID, 7, testSpecRevision, testNodeURL); err == nil {
		t.Fatal("oversize credential accepted")
	}

	if runtime.GOOS != "windows" {
		target := writeControlCredentialFixture(t, credentialCertificateOptions{}, nil, "")
		link := filepath.Join(t.TempDir(), "linked.credential")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("create credential symlink: %v", err)
		}
		if _, err := LoadControlCredential(link, testTenantID, testConnectorID, 7, testSpecRevision, testNodeURL); err == nil {
			t.Fatal("credential symlink accepted")
		}

		if err := os.Chmod(target, 0o644); err != nil {
			t.Fatalf("chmod credential: %v", err)
		}
		if _, err := LoadControlCredential(target, testTenantID, testConnectorID, 7, testSpecRevision, testNodeURL); err == nil {
			t.Fatal("world-readable credential accepted")
		}
	}
}

type credentialCertificateOptions struct {
	commonName       string
	dnsNames         []string
	extendedKeyUsage []x509.ExtKeyUsage
	expired          bool
}

type controlCredentialFixture struct {
	path                string
	serverRoot          *x509.Certificate
	connectorIssuerRoot *x509.Certificate
}

func writeControlCredentialFixture(
	t *testing.T,
	options credentialCertificateOptions,
	mutate func(map[string]any),
	suffix string,
) string {
	return writeControlCredentialFixtureForSchema(
		t,
		ControlCredentialSchemaVersionV2,
		options,
		mutate,
		suffix,
	).path
}

func writeControlCredentialFixtureForSchema(
	t *testing.T,
	schemaVersion uint16,
	options credentialCertificateOptions,
	mutate func(map[string]any),
	suffix string,
) controlCredentialFixture {
	t.Helper()

	now := time.Now().Truncate(time.Second)
	serverRoot, _, serverRootDER := createControlCredentialTestRoot(t, "Dirextalk control server root", 1, now)
	connectorIssuerRoot, connectorIssuerPrivate, connectorIssuerRootDER := createControlCredentialTestRoot(t, "Dirextalk connector issuer root", 2, now)

	leafPublic, leafPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	identityURI, err := url.Parse("spiffe://dirextalk.internal/v1/tenants/" + testTenantID + "/connectors/" + testConnectorID)
	if err != nil {
		t.Fatalf("parse identity URI: %v", err)
	}
	notBefore, notAfter := now.Add(-time.Minute), now.Add(time.Hour)
	if options.expired {
		notBefore, notAfter = now.Add(-2*time.Hour), now.Add(-time.Hour)
	}
	usage := options.extendedKeyUsage
	if usage == nil {
		usage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	leafTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: options.commonName},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           usage,
		URIs:                  []*url.URL{identityURI},
		DNSNames:              options.dnsNames,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, connectorIssuerRoot, leafPublic, connectorIssuerPrivate)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(leafPrivate)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	fingerprint := sha256.Sum256(leafDER)
	value := map[string]any{
		"schema_version":          schemaVersion,
		"tenant_id":               testTenantID,
		"connector_id":            testConnectorID,
		"generation":              7,
		"credential_revision":     11,
		"server_name":             "control.example.com",
		"certificate_chain_pem":   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
		"private_key_pem":         string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})),
		"leaf_fingerprint_sha256": hex.EncodeToString(fingerprint[:]),
	}
	switch schemaVersion {
	case ControlCredentialSchemaVersionV1:
		value["root_ca_pem"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: connectorIssuerRootDER}))
	case ControlCredentialSchemaVersionV2:
		value["server_root_ca_pem"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverRootDER}))
		value["connector_issuer_root_ca_pem"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: connectorIssuerRootDER}))
	default:
		t.Fatalf("unsupported test credential schema v%d", schemaVersion)
	}
	if mutate != nil {
		mutate(value)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal credential JSON: %v", err)
	}
	path := filepath.Join(t.TempDir(), "control.credential")
	if err := os.WriteFile(path, append(encoded, suffix...), 0o600); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	restrictCredentialFixture(t, path)
	return controlCredentialFixture{
		path:                path,
		serverRoot:          serverRoot,
		connectorIssuerRoot: connectorIssuerRoot,
	}
}

func createControlCredentialTestRoot(
	t *testing.T,
	commonName string,
	serial int64,
	now time.Time,
) (*x509.Certificate, ed25519.PrivateKey, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate %s key: %v", commonName, err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(2 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatalf("create %s certificate: %v", commonName, err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse %s certificate: %v", commonName, err)
	}
	return certificate, privateKey, der
}

func certificatePoolContainsSubject(pool *x509.CertPool, certificate *x509.Certificate) bool {
	if pool == nil || certificate == nil {
		return false
	}
	for _, subject := range pool.Subjects() {
		if bytes.Equal(subject, certificate.RawSubject) {
			return true
		}
	}
	return false
}
