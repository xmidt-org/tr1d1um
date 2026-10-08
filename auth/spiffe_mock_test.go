// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/spiffe/go-spiffe/v2/proto/spiffe/workload"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const mockSPIFFEID = "spiffe://example.org/tr1d1um"

// mockWorkloadAPI is an in-process SPIFFE Workload API, served over a unix
// socket, that issues JWT-SVIDs as a SPIRE agent would.  It lets tests run
// the real go-spiffe client end to end without a SPIRE deployment.
type mockWorkloadAPI struct {
	workload.UnimplementedSpiffeWorkloadAPIServer

	t   *testing.T
	key *ecdsa.PrivateKey

	// Socket is the bare path of the unix socket the mock listens on.
	Socket string

	lock      sync.Mutex
	audiences [][]string
	issued    []string
}

func newMockWorkloadAPI(t *testing.T) *mockWorkloadAPI {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// A unix socket path is limited to about 100 bytes, which t.TempDir can
	// exceed for long test names.
	dir, err := os.MkdirTemp("", "spiffe")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	m := &mockWorkloadAPI{t: t, key: key, Socket: filepath.Join(dir, "agent.sock")}

	l, err := net.Listen("unix", m.Socket)
	require.NoError(t, err)

	srv := grpc.NewServer()
	workload.RegisterSpiffeWorkloadAPIServer(srv, m)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(srv.Stop)

	return m
}

// FetchJWTSVID issues an SVID for the requested audience.  Like a SPIRE
// agent, it refuses a call without the Workload API security header.
func (m *mockWorkloadAPI) FetchJWTSVID(ctx context.Context, req *workload.JWTSVIDRequest) (*workload.JWTSVIDResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get("workload.spiffe.io"); len(v) != 1 || v[0] != "true" {
		return nil, status.Error(codes.InvalidArgument, "security header missing from request")
	}

	token, err := jwt.NewBuilder().
		Subject(mockSPIFFEID).
		Audience(req.Audience).
		IssuedAt(time.Now()).
		Expiration(time.Now().Add(5 * time.Minute)).
		Build()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.ES256(), m.key))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	m.lock.Lock()
	defer m.lock.Unlock()
	m.audiences = append(m.audiences, req.Audience)
	m.issued = append(m.issued, string(signed))

	return &workload.JWTSVIDResponse{
		Svids: []*workload.JWTSVID{{SpiffeId: mockSPIFFEID, Svid: string(signed)}},
	}, nil
}

// Issued returns the SVIDs handed out and the audiences each was requested
// for, in order.
func (m *mockWorkloadAPI) Issued() ([]string, [][]string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	return append([]string(nil), m.issued...), append([][]string(nil), m.audiences...)
}
