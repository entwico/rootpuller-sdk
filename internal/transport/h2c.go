package transport

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

var errUnsupportedScheme = errors.New("must use http or https scheme")

// NewHTTPClient builds the HTTP/2 client implied by the base URL scheme:
// "http" dials cleartext HTTP/2 (h2c with prior knowledge, the gRPC
// in-cluster default), "https" negotiates TLS with the given config (nil
// means system defaults).
func NewHTTPClient(baseURL string, tlsConfig *tls.Config) (*http.Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("rootpuller: invalid base URL %q: %w", baseURL, err)
	}

	protocols := new(http.Protocols)

	switch u.Scheme {
	case "http":
		// HTTP/2 prior knowledge without TLS; HTTP/1 stays off so the
		// bidirectional gRPC streams never downgrade.
		protocols.SetUnencryptedHTTP2(true)

		return &http.Client{Transport: &http.Transport{Protocols: protocols}}, nil
	case "https":
		protocols.SetHTTP2(true)

		return &http.Client{Transport: &http.Transport{
			TLSClientConfig:   tlsConfig,
			ForceAttemptHTTP2: true,
			Protocols:         protocols,
		}}, nil
	default:
		return nil, fmt.Errorf("rootpuller: base URL %q %w", baseURL, errUnsupportedScheme)
	}
}
