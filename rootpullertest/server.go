// Package rootpullertest provides an in-process rootpuller-api stand-in
// for testing SDK consumers. NewServer starts a cleartext HTTP/2 (h2c)
// server speaking the same gRPC protocol as the real API, so it can be
// dialed with rootpuller.New(srv.URL) — the exact production code path.
package rootpullertest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Service is implemented by the fake services in this package.
type Service interface {
	register(mux *http.ServeMux)
}

// Server is an in-process fake rootpuller-api.
type Server struct {
	// URL is the base URL to pass to rootpuller.New.
	URL string
}

// NewServer starts an h2c test server hosting the given fake services and
// shuts it down when the test ends.
func NewServer(tb testing.TB, services ...Service) *Server {
	tb.Helper()

	mux := http.NewServeMux()
	for _, svc := range services {
		svc.register(mux)
	}

	return startServer(tb, mux)
}

// NewServerWithMux starts an h2c test server for a hand-built mux. It is
// the low-level hook used by the SDK's own protocol tests; consumers
// normally use NewServer with the typed fakes.
func NewServerWithMux(tb testing.TB, mux *http.ServeMux) *Server {
	tb.Helper()

	return startServer(tb, mux)
}

func startServer(tb testing.TB, mux *http.ServeMux) *Server {
	tb.Helper()

	srv := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true) // cleartext HTTP/2, like the real gRPC listener
	srv.Config.Protocols = protocols
	srv.Start()
	tb.Cleanup(srv.Close)

	return &Server{URL: srv.URL}
}
