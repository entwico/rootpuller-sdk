package chunker_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/oauth2"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/durationpb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/chunker"
	chunkerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker/chunkerconnect"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

var (
	errKeycloakUnreachable = errors.New("keycloak unreachable")
	errDeploymentSaturated = errors.New("deployment saturated")
)

func newService(t *testing.T, baseURL string) *chunker.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return chunker.NewService(sdk)
}

func TestChunkTokenRoundTrip(t *testing.T) {
	t.Parallel()

	var gotMethod string

	srv := rootpullertest.NewServer(t, &rootpullertest.Chunker{
		ChunkFunc: func(method string, texts []string) ([][]rootpullersdk.TextChunk, error) {
			gotMethod = method

			out := make([][]rootpullersdk.TextChunk, len(texts))
			for i, text := range texts {
				out[i] = []rootpullersdk.TextChunk{{Text: text, EndIndex: len(text), TokenCount: i + 1}}
			}

			return out, nil
		},
	})

	svc := newService(t, srv.URL)

	chunks, err := svc.ChunkToken(t.Context(), []string{"hello world", "second text"}, &chunker.TokenOptions{
		Tokenizer: chunker.TokenizerGPT2,
		ChunkSize: 128,
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotMethod != "ChunkToken" {
		t.Fatalf("server saw method %q, want ChunkToken", gotMethod)
	}

	if len(chunks) != 2 {
		t.Fatalf("got %d results, want 2", len(chunks))
	}

	if chunks[0][0].Text != "hello world" || chunks[1][0].TokenCount != 2 {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
}

// headerCapturingChunker records selected request headers of the last call.
type headerCapturingChunker struct {
	chunkerconnect.UnimplementedTextChunkerServiceHandler

	headers chan http.Header
}

func (h *headerCapturingChunker) ChunkToken(_ context.Context, req *connect.Request[chunkerpb.ChunkTokenRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	h.headers <- req.Header()

	return connect.NewResponse(&chunkerpb.TextChunkResponse{}), nil
}

func newHeaderCapturingServer(t *testing.T) (*rootpullertest.Server, chan http.Header) {
	t.Helper()

	handler := &headerCapturingChunker{headers: make(chan http.Header, 1)}
	mux := http.NewServeMux()
	mux.Handle(chunkerconnect.NewTextChunkerServiceHandler(handler))

	return rootpullertest.NewServerWithMux(t, mux), handler.headers
}

func TestAuthAndRoutingHeaders(t *testing.T) {
	t.Parallel()

	srv, headers := newHeaderCapturingServer(t)

	sdk, err := rootpullersdk.New(srv.URL, rootpullersdk.WithToken("test-token"))
	if err != nil {
		t.Fatal(err)
	}

	svc := chunker.NewService(sdk, chunker.WithDeployment("cloudrun"))
	if _, err := svc.ChunkToken(t.Context(), []string{"x"}, nil); err != nil {
		t.Fatal(err)
	}

	h := <-headers
	if got := h.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
	}

	if got := h.Get("Rootpuller-Deployment"); got != "cloudrun" {
		t.Errorf("rootpuller-deployment = %q, want cloudrun", got)
	}

	// Per-call context overrides beat the service default.
	ctx := rootpullersdk.ContextWithDeployment(t.Context(), "local")
	if _, err := svc.ChunkToken(ctx, []string{"x"}, nil); err != nil {
		t.Fatal(err)
	}

	h = <-headers
	if got := h.Get("Rootpuller-Deployment"); got != "local" {
		t.Errorf("override rootpuller-deployment = %q, want local", got)
	}

	// A service built without WithDeployment sends no routing header.
	plain := chunker.NewService(sdk)
	if _, err := plain.ChunkToken(t.Context(), []string{"x"}, nil); err != nil {
		t.Fatal(err)
	}

	h = <-headers
	if got := h.Get("Rootpuller-Deployment"); got != "" {
		t.Errorf("plain service rootpuller-deployment = %q, want unset", got)
	}
}

func TestNoAuthHeaderWithoutTokenSource(t *testing.T) {
	t.Parallel()

	srv, headers := newHeaderCapturingServer(t)

	svc := newService(t, srv.URL)

	if _, err := svc.ChunkToken(t.Context(), []string{"x"}, nil); err != nil {
		t.Fatal(err)
	}

	h := <-headers
	if got := h.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty (no token source configured)", got)
	}

	if got := h.Get("Rootpuller-Deployment"); got != "" {
		t.Errorf("rootpuller-deployment = %q, want unset", got)
	}
}

type failingTokenSource struct{}

func (failingTokenSource) Token() (*oauth2.Token, error) {
	return nil, errKeycloakUnreachable
}

func TestTokenSourceFailure(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Chunker{})

	sdk, err := rootpullersdk.New(srv.URL, rootpullersdk.WithTokenSource(failingTokenSource{}))
	if err != nil {
		t.Fatal(err)
	}

	svc := chunker.NewService(sdk)

	_, err = svc.ChunkToken(t.Context(), []string{"x"}, nil)
	if !errors.Is(err, rootpullersdk.ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}

	if ae, ok := errors.AsType[*rootpullersdk.Error](err); !ok || ae.Code != connect.CodeUnauthenticated {
		t.Fatalf("err = %#v, want *rootpullersdk.Error with CodeUnauthenticated", err)
	}
}

func TestServerErrorWithRetryInfo(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Chunker{
		ChunkFunc: func(string, []string) ([][]rootpullersdk.TextChunk, error) {
			cerr := connect.NewError(connect.CodeResourceExhausted, errDeploymentSaturated)
			if detail, derr := connect.NewErrorDetail(&errdetails.RetryInfo{
				RetryDelay: durationpb.New(1500 * time.Millisecond),
			}); derr == nil {
				cerr.AddDetail(detail)
			}

			return nil, cerr
		},
	})

	svc := newService(t, srv.URL)

	_, err := svc.ChunkToken(t.Context(), []string{"x"}, nil)
	if !errors.Is(err, rootpullersdk.ErrResourceExhausted) {
		t.Fatalf("err = %v, want ErrResourceExhausted", err)
	}

	ae, ok := errors.AsType[*rootpullersdk.Error](err)
	if !ok {
		t.Fatalf("err = %#v, want *rootpullersdk.Error", err)
	}

	if ae.RetryAfter != 1500*time.Millisecond {
		t.Errorf("RetryAfter = %v, want 1.5s", ae.RetryAfter)
	}

	if ae.Procedure != chunkerconnect.TextChunkerServiceChunkTokenProcedure {
		t.Errorf("Procedure = %q, want %q", ae.Procedure, chunkerconnect.TextChunkerServiceChunkTokenProcedure)
	}

	if ae.Message != "deployment saturated" {
		t.Errorf("Message = %q, want %q", ae.Message, "deployment saturated")
	}
}

func TestInvalidEnumFailsLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	_, err := svc.ChunkToken(t.Context(), []string{"x"}, &chunker.TokenOptions{
		Tokenizer: chunker.Tokenizer("bogus"),
	})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestAllMethodsDispatch(t *testing.T) {
	t.Parallel()

	var methods []string

	srv := rootpullertest.NewServer(t, &rootpullertest.Chunker{
		ChunkFunc: func(method string, texts []string) ([][]rootpullersdk.TextChunk, error) {
			methods = append(methods, method)

			return make([][]rootpullersdk.TextChunk, len(texts)), nil
		},
	})

	svc := newService(t, srv.URL)

	ctx := t.Context()
	texts := []string{"x"}

	calls := []struct {
		name string
		call func() error
	}{
		{"ChunkSemantic", func() error {
			_, e := svc.ChunkSemantic(ctx, texts, nil)

			return e
		}},
		{"ChunkToken", func() error {
			_, e := svc.ChunkToken(ctx, texts, nil)

			return e
		}},
		{"ChunkSentence", func() error {
			_, e := svc.ChunkSentence(ctx, texts, nil)

			return e
		}},
		{"ChunkCode", func() error {
			_, e := svc.ChunkCode(ctx, texts, nil)

			return e
		}},
		{"ChunkRecursive", func() error {
			_, e := svc.ChunkRecursive(ctx, texts, nil)

			return e
		}},
		{"ChunkLate", func() error {
			_, e := svc.ChunkLate(ctx, texts, nil)

			return e
		}},
		{"ChunkNeural", func() error {
			_, e := svc.ChunkNeural(ctx, texts, nil)

			return e
		}},
		{"ChunkSlumber", func() error {
			_, e := svc.ChunkSlumber(ctx, texts, nil)

			return e
		}},
	}
	for _, tc := range calls {
		if err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}

	for i, tc := range calls {
		if methods[i] != tc.name {
			t.Errorf("call %d dispatched to %q, want %q", i, methods[i], tc.name)
		}
	}
}

// capturingSemantic records the proto request of each ChunkSemantic call.
type capturingSemantic struct {
	chunkerconnect.UnimplementedTextChunkerServiceHandler

	requests chan *chunkerpb.ChunkSemanticRequest
}

func (h *capturingSemantic) ChunkSemantic(_ context.Context, req *connect.Request[chunkerpb.ChunkSemanticRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	h.requests <- req.Msg

	return connect.NewResponse(&chunkerpb.TextChunkResponse{}), nil
}

func TestSemanticOptionsRoundTrip(t *testing.T) {
	t.Parallel()

	handler := &capturingSemantic{requests: make(chan *chunkerpb.ChunkSemanticRequest, 1)}
	mux := http.NewServeMux()
	mux.Handle(chunkerconnect.NewTextChunkerServiceHandler(handler))
	srv := rootpullertest.NewServerWithMux(t, mux)

	svc := newService(t, srv.URL)

	threshold := float32(0.7)

	_, err := svc.ChunkSemantic(t.Context(), []string{"a", "b"}, &chunker.SemanticOptions{
		Model:     "minishlab/potion-base-32M",
		Threshold: &threshold,
		ChunkSize: 256,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg := <-handler.requests
	if got := msg.GetTexts(); len(got) != 2 || got[0] != "a" {
		t.Errorf("Texts = %v, want [a b]", got)
	}

	if msg.GetModel() != "minishlab/potion-base-32M" {
		t.Errorf("Model = %q, want minishlab/potion-base-32M", msg.GetModel())
	}

	if msg.Threshold == nil || msg.GetThreshold() != 0.7 {
		t.Errorf("Threshold = %v, want 0.7", msg.Threshold)
	}

	if msg.ChunkSize == nil || msg.GetChunkSize() != 256 {
		t.Errorf("ChunkSize = %v, want 256", msg.ChunkSize)
	}

	if msg.SimilarityWindow != nil || msg.SkipWindow != nil || msg.FilterTolerance != nil {
		t.Errorf("zero-value counts must stay unset: %+v", msg)
	}

	// Nil options: every optional proto field stays unset.
	if _, err := svc.ChunkSemantic(t.Context(), []string{"a"}, nil); err != nil {
		t.Fatal(err)
	}

	msg = <-handler.requests
	if msg.Threshold != nil || msg.ChunkSize != nil || msg.GetModel() != "" || msg.GetNormalize() != nil {
		t.Errorf("optional fields must be unset for nil options: %+v", msg)
	}
}
