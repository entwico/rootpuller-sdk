package transport_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// fakeTarget is deliberately unresolvable so a request can only succeed by
// going through the proxy tunnel, never by a direct dial.
const (
	fakeTarget    = "rootpuller-proxy-test.invalid:8755"
	fakeTargetURL = "http://" + fakeTarget + "/ping"
)

// startH2CBackend serves prior-knowledge cleartext HTTP/2, reporting the
// negotiated protocol and Host of each request on the returned channel so
// tests can prove the request arrived as h2c.
func startH2CBackend(t *testing.T) (string, <-chan string) {
	t.Helper()

	listenConfig := new(net.ListenConfig)

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen backend: %v", err)
	}

	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)

	requests := make(chan string, 1)
	server := &http.Server{
		Protocols:         protocols,
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- fmt.Sprintf("proto=%s host=%s", r.Proto, r.Host)

			_, _ = io.WriteString(w, "ok")
		}),
	}

	go func() { _ = server.Serve(listener) }()

	t.Cleanup(func() { server.Close() })

	return listener.Addr().String(), requests
}

// startConnectProxy accepts one connection, validates the CONNECT request,
// and splices the tunnel to backendAddr. It reports the CONNECT request on
// the returned channel.
func startConnectProxy(t *testing.T, backendAddr string) (string, <-chan *http.Request) {
	t.Helper()

	listenConfig := new(net.ListenConfig)

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}

	t.Cleanup(func() { listener.Close() })

	ctx := t.Context()
	requests := make(chan *http.Request, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)

		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}

		requests <- req

		if req.Method != http.MethodConnect {
			_, _ = io.WriteString(conn, "HTTP/1.1 405 Method Not Allowed\r\n\r\n")

			return
		}

		var dialer net.Dialer

		backendConn, err := dialer.DialContext(ctx, "tcp", backendAddr)
		if err != nil {
			_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")

			return
		}
		defer backendConn.Close()

		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n")

		go func() { _, _ = io.Copy(backendConn, reader) }()

		_, _ = io.Copy(conn, backendConn)
	}()

	return listener.Addr().String(), requests
}

// TestNewHTTPClientH2CTunnelsThroughEnvProxy is the end-to-end case: an h2c
// client built by NewHTTPClient reaches an unresolvable cluster host through
// the HTTP_PROXY CONNECT tunnel. It must stay the only env-driven proxy test
// in this binary — http.ProxyFromEnvironment caches the environment on first
// use, so a second test with different values would read stale config.
func TestNewHTTPClientH2CTunnelsThroughEnvProxy(t *testing.T) {
	backendAddr, sawRequest := startH2CBackend(t)
	proxyAddr, sawConnect := startConnectProxy(t, backendAddr)

	t.Setenv("HTTP_PROXY", "http://"+proxyAddr)
	t.Setenv("NO_PROXY", "")

	client, err := transport.NewHTTPClient("http://"+fakeTarget, nil)
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, fakeTargetURL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if string(body) != "ok" {
		t.Errorf("backend replied %q, want %q", body, "ok")
	}

	if got, want := <-sawRequest, "proto=HTTP/2.0 host="+fakeTarget; got != want {
		t.Errorf("backend saw %q, want %q", got, want)
	}

	select {
	case connectReq := <-sawConnect:
		if connectReq.Host != fakeTarget {
			t.Errorf("proxy saw CONNECT %q, want %q", connectReq.Host, fakeTarget)
		}
	case <-time.After(2 * time.Second):
		t.Error("proxy never received a CONNECT request")
	}
}

func TestH2CDialerDirectWhenNoProxy(t *testing.T) {
	t.Parallel()

	backendAddr, _ := startH2CBackend(t)

	dialer := &transport.H2CDialer{ProxyForTarget: func(string) (*url.URL, error) {
		return nil, nil
	}}

	conn, err := dialer.Dial(t.Context(), "tcp", backendAddr)
	if err != nil {
		t.Fatalf("direct dial: %v", err)
	}

	conn.Close()
}

func TestH2CDialerSendsProxyAuth(t *testing.T) {
	t.Parallel()

	backendAddr, _ := startH2CBackend(t)
	proxyAddr, sawConnect := startConnectProxy(t, backendAddr)

	dialer := &transport.H2CDialer{ProxyForTarget: func(string) (*url.URL, error) {
		return url.Parse("http://user:secret@" + proxyAddr)
	}}

	conn, err := dialer.Dial(t.Context(), "tcp", fakeTarget)
	if err != nil {
		t.Fatalf("dial through proxy: %v", err)
	}

	conn.Close()

	req := <-sawConnect
	// base64("user:secret")
	if got, want := req.Header.Get("Proxy-Authorization"), "Basic dXNlcjpzZWNyZXQ="; got != want {
		t.Errorf("Proxy-Authorization = %q, want %q", got, want)
	}
}

func TestH2CDialerReportsConnectRefusal(t *testing.T) {
	t.Parallel()

	listenConfig := new(net.ListenConfig)

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}

	t.Cleanup(func() { listener.Close() })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		_, _ = http.ReadRequest(bufio.NewReader(conn))
		_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
	}()

	dialer := &transport.H2CDialer{ProxyForTarget: func(string) (*url.URL, error) {
		return url.Parse("http://" + listener.Addr().String())
	}}

	_, err = dialer.Dial(t.Context(), "tcp", fakeTarget)
	if err == nil {
		t.Fatal("dial succeeded, want CONNECT refusal error")
	}

	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q does not mention the proxy status", err)
	}
}

func TestConnectTunnelKeepsPipelinedBytes(t *testing.T) {
	t.Parallel()

	clientSide, proxySide := net.Pipe()

	t.Cleanup(func() { clientSide.Close() })
	t.Cleanup(func() { proxySide.Close() })

	go func() {
		// Consume the CONNECT request, then answer with tunnel bytes
		// pipelined directly behind the response.
		buf := make([]byte, 1024)
		_, _ = proxySide.Read(buf)
		_, _ = io.WriteString(proxySide, "HTTP/1.1 200 OK\r\n\r\npipelined")
	}()

	proxyURL := &url.URL{Scheme: "http", Host: "proxy.invalid:80"}

	conn, err := transport.ConnectTunnel(t.Context(), clientSide, proxyURL, fakeTarget)
	if err != nil {
		t.Fatalf("ConnectTunnel: %v", err)
	}

	got := make([]byte, len("pipelined"))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read tunnel bytes: %v", err)
	}

	if string(got) != "pipelined" {
		t.Errorf("tunnel read %q, want %q", got, "pipelined")
	}
}
