// Package transport holds the shared plumbing behind every service client:
// the HTTP/2 client (h2c or TLS), the connect client options, and the
// interceptors for auth and routing headers.
package transport

import (
	"net/http"

	"connectrpc.com/connect"
)

// Core is handed to every service package's NewFromCore constructor. It
// lives in internal/ so rootpuller.New stays the only public entry point.
type Core struct {
	HTTPClient *http.Client
	BaseURL    string
	ClientOpts []connect.ClientOption
}
