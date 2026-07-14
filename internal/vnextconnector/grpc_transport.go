package vnextconnector

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	controlv1 "github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector/agentcontrolv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// NewEnrollmentConnection builds the server-authenticated TLS connection used
// only for the one-time enrollment RPC. It deliberately takes trust material
// independent from the control mTLS credential issuer.
func NewEnrollmentConnection(nodeURL, serverName string, rootCAPEM []byte) (*grpc.ClientConn, error) {
	if err := validateEnrollmentEndpoint(nodeURL, serverName, "enrollment"); err != nil {
		return nil, err
	}
	_, roots, err := enrollmentTLSRoots(rootCAPEM)
	if err != nil {
		return nil, errors.New("vnext connector: invalid enrollment root CA")
	}
	target, err := controlTarget(nodeURL)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
		ServerName: serverName,
		RootCAs:    roots,
		NextProtos: []string{"h2"},
	}
	return grpc.NewClient(
		target,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  250 * time.Millisecond,
				Multiplier: 1.6,
				Jitter:     0.2,
				MaxDelay:   2 * time.Second,
			},
			MinConnectTimeout: 5 * time.Second,
		}),
		grpc.WithUserAgent("dirextalk-connect/vnext-enrollment"),
	)
}

// NewControlConnection builds one outbound-only mTLS connection. grpc.NewClient
// is intentionally non-blocking; the control stream's Hello/ConnectLease
// exchange is the readiness boundary.
func NewControlConnection(nodeURL string, credential *ControlCredential) (*grpc.ClientConn, error) {
	if credential == nil {
		return nil, errors.New("vnext connector: missing control credential")
	}
	tlsConfig := credential.TLSConfig()
	if tlsConfig == nil {
		return nil, errors.New("vnext connector: missing control credential")
	}
	target, err := controlTarget(nodeURL)
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(
		target,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  500 * time.Millisecond,
				Multiplier: 1.6,
				Jitter:     0.2,
				MaxDelay:   15 * time.Second,
			},
			MinConnectTimeout: 10 * time.Second,
		}),
		grpc.WithUserAgent("dirextalk-connect/vnext"),
	)
}

func controlTarget(value string) (string, error) {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Fragment != "" || parsed.RawQuery != "" || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.Hostname() == "" {
		return "", errors.New("vnext connector: control node must be an origin HTTPS URL")
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	authority := net.JoinHostPort(strings.ToLower(parsed.Hostname()), port)
	return "dns:///" + authority, nil
}

// GRPCStreamOpener applies the frozen per-frame ceiling at the generated gRPC
// boundary. The connection itself is created by DialControl after credential
// verification.
func GRPCStreamOpener(client controlv1.ConnectorControlClient) StreamOpener {
	return func(ctx context.Context) (ControlStream, error) {
		return client.Control(
			ctx,
			grpc.MaxCallRecvMsgSize(maxAgentControlMessageBytes),
			grpc.MaxCallSendMsgSize(maxAgentControlMessageBytes),
		)
	}
}

func grpcStatusIsPermanent(err error) bool {
	code := status.Code(err)
	switch code {
	case codes.InvalidArgument,
		codes.NotFound,
		codes.AlreadyExists,
		codes.PermissionDenied,
		codes.FailedPrecondition,
		codes.OutOfRange,
		codes.Unimplemented,
		codes.DataLoss,
		codes.Unauthenticated:
		return true
	default:
		return false
	}
}
