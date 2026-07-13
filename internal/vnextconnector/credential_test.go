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

func TestLoadControlCredentialBuildsStrictTLSConfig(t *testing.T) {
	path := writeControlCredentialFixture(t, credentialCertificateOptions{}, nil, "")

	credential, err := LoadControlCredential(
		path,
		testTenantID,
		testConnectorID,
		7,
		testNodeURL,
	)
	if err != nil {
		t.Fatalf("LoadControlCredential: %v", err)
	}
	if credential.SchemaVersion != 1 || credential.TenantID != testTenantID || credential.ConnectorID != testConnectorID {
		t.Fatalf("credential identity = %+v", credential)
	}
	if credential.Generation != 7 || credential.CredentialRevision != 11 {
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
	for _, secret := range []string{"PRIVATE KEY", "CERTIFICATE", "root_ca_pem", "private_key_pem"} {
		if strings.Contains(formatted, secret) {
			t.Fatalf("formatted credential leaked %q: %s", secret, formatted)
		}
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
			if _, err := LoadControlCredential(path, testTenantID, testConnectorID, 7, testNodeURL); err == nil {
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
	if _, err := LoadControlCredential(duplicatePath, testTenantID, testConnectorID, 7, testNodeURL); err == nil {
		t.Fatal("duplicate credential JSON field accepted")
	}
}

func TestLoadControlCredentialRejectsUnsafeFiles(t *testing.T) {
	oversize := filepath.Join(t.TempDir(), "oversize.credential")
	if err := os.WriteFile(oversize, make([]byte, 64*1024+1), 0o600); err != nil {
		t.Fatalf("write oversize credential: %v", err)
	}
	if _, err := LoadControlCredential(oversize, testTenantID, testConnectorID, 7, testNodeURL); err == nil {
		t.Fatal("oversize credential accepted")
	}

	if runtime.GOOS != "windows" {
		target := writeControlCredentialFixture(t, credentialCertificateOptions{}, nil, "")
		link := filepath.Join(t.TempDir(), "linked.credential")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("create credential symlink: %v", err)
		}
		if _, err := LoadControlCredential(link, testTenantID, testConnectorID, 7, testNodeURL); err == nil {
			t.Fatal("credential symlink accepted")
		}

		if err := os.Chmod(target, 0o644); err != nil {
			t.Fatalf("chmod credential: %v", err)
		}
		if _, err := LoadControlCredential(target, testTenantID, testConnectorID, 7, testNodeURL); err == nil {
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

func writeControlCredentialFixture(
	t *testing.T,
	options credentialCertificateOptions,
	mutate func(map[string]any),
	suffix string,
) string {
	t.Helper()

	now := time.Now()
	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate root key: %v", err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Dirextalk test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(2 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootPublic, rootPrivate)
	if err != nil {
		t.Fatalf("create root certificate: %v", err)
	}
	rootCertificate, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatalf("parse root certificate: %v", err)
	}

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
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, rootCertificate, leafPublic, rootPrivate)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(leafPrivate)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	fingerprint := sha256.Sum256(leafDER)
	value := map[string]any{
		"schema_version":          1,
		"tenant_id":               testTenantID,
		"connector_id":            testConnectorID,
		"generation":              7,
		"credential_revision":     11,
		"server_name":             "control.example.com",
		"root_ca_pem":             string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})),
		"certificate_chain_pem":   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
		"private_key_pem":         string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})),
		"leaf_fingerprint_sha256": hex.EncodeToString(fingerprint[:]),
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
	return path
}
