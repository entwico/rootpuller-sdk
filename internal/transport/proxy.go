package transport

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
)

var (
	errUnsupportedProxyScheme = errors.New("must use an http or https proxy")
	errProxyConnectRefused    = errors.New("proxy refused CONNECT")
)

// maxConnectResponseBytes caps how much of the proxy's CONNECT response is
// read during parsing, mirroring the bound net/http puts on its own tunnels.
const maxConnectResponseBytes = 1 << 20

// H2CDialer dials cleartext HTTP/2 connections, tunneling through the
// HTTP_PROXY/HTTPS_PROXY from the environment via CONNECT when one applies.
// The transport's Proxy field can't be used for h2c: with HTTP/1 disabled
// the transport would send the HTTP/2 preface to the proxy itself instead
// of tunneling to the target.
type H2CDialer struct {
	// ProxyForTarget returns the proxy to use for a target host:port, or
	// nil for a direct connection. Injectable so tests can bypass
	// http.ProxyFromEnvironment, which caches the environment on first use.
	ProxyForTarget func(addr string) (*url.URL, error)
}

// proxyFromEnvironment resolves the proxy for a target host:port from
// HTTP_PROXY/NO_PROXY, matching what the transport would do for a plain
// http request to that host.
func proxyFromEnvironment(addr string) (*url.URL, error) {
	return http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: schemeHTTP, Host: addr}})
}

// Dial connects to addr, tunneling through the resolved proxy when one
// applies and dialing directly otherwise.
func (d *H2CDialer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	proxyURL, err := d.ProxyForTarget(addr)
	if err != nil {
		return nil, fmt.Errorf("rootpuller: resolve proxy for %s: %w", addr, err)
	}

	var dialer net.Dialer
	if proxyURL == nil {
		return dialer.DialContext(ctx, network, addr)
	}

	conn, err := dialer.DialContext(ctx, network, proxyDialAddr(proxyURL))
	if err != nil {
		return nil, fmt.Errorf("rootpuller: dial proxy %s: %w", proxyURL.Host, err)
	}

	tunneled, err := ConnectTunnel(ctx, conn, proxyURL, addr)
	if err != nil {
		conn.Close()

		return nil, err
	}

	return tunneled, nil
}

// proxyDialAddr is the proxy's host:port, defaulting the port from its scheme.
func proxyDialAddr(proxyURL *url.URL) string {
	if proxyURL.Port() != "" {
		return proxyURL.Host
	}

	if proxyURL.Scheme == schemeHTTPS {
		return net.JoinHostPort(proxyURL.Hostname(), "443")
	}

	return net.JoinHostPort(proxyURL.Hostname(), "80")
}

// ConnectTunnel turns conn (a raw connection to the proxy) into a tunnel to
// target by issuing an HTTP/1.1 CONNECT, so the h2c preface flows end to end.
func ConnectTunnel(ctx context.Context, conn net.Conn, proxyURL *url.URL, target string) (net.Conn, error) {
	switch proxyURL.Scheme {
	case schemeHTTP:
	case schemeHTTPS:
		// No ALPN, so CONNECT stays HTTP/1.1 even if the proxy speaks h2.
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: proxyURL.Hostname(),
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("rootpuller: TLS handshake with proxy %s: %w", proxyURL.Host, err)
		}

		conn = tlsConn
	default:
		return nil, fmt.Errorf("rootpuller: proxy %s %w", proxyURL.Redacted(), errUnsupportedProxyScheme)
	}

	// Closing conn is the only way to unblock the exchange below when ctx
	// is canceled or times out; Dial closes conn on any error return.
	watchDone := make(chan struct{})
	defer close(watchDone)

	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-watchDone:
		}
	}()

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target},
		Host:   target,
		Header: make(http.Header),
	}

	if user := proxyURL.User; user != nil {
		password, _ := user.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(user.Username() + ":" + password))
		req.Header.Set("Proxy-Authorization", "Basic "+credentials)
	}

	if err := req.Write(conn); err != nil {
		return nil, fmt.Errorf("rootpuller: CONNECT %s via proxy %s: %w", target, proxyURL.Host, cmp.Or(ctx.Err(), err))
	}

	reader := bufio.NewReader(io.LimitReader(conn, maxConnectResponseBytes))

	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, fmt.Errorf("rootpuller: CONNECT %s via proxy %s: %w", target, proxyURL.Host, cmp.Or(ctx.Err(), err))
	}

	// A 2xx CONNECT response has no body, so this never touches the wire.
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rootpuller: %w %s via proxy %s: %s", errProxyConnectRefused, target, proxyURL.Host, resp.Status)
	}

	// The proxy may have pipelined tunnel bytes behind its response; drain
	// the reader's buffer before handing reads back to the connection.
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}

	return conn, nil
}

// bufferedConn drains bytes the CONNECT response reader over-read before
// handing reads back to the underlying connection.
type bufferedConn struct {
	net.Conn

	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if c.reader != nil {
		if c.reader.Buffered() > 0 {
			return c.reader.Read(p)
		}

		c.reader = nil
	}

	return c.Conn.Read(p)
}
