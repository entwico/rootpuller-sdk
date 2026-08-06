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

	rootpuller "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/chunker"
	"github.com/entwico/rootpuller-sdk/common"
	chunkerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker/chunkerconnect"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

var (
	errKeycloakUnreachable = errors.New("keycloak unreachable")
	errDeploymentSaturated = errors.New("deployment saturated")
)

func TestChunkTokenRoundTrip(t *testing.T) {
	t.Parallel()

	var gotMethod string

	srv := rootpullertest.NewServer(t, &rootpullertest.Chunker{
		ChunkFunc: func(method string, texts []string) ([][]common.TextChunk, error) {
			gotMethod = method

			out := make([][]common.TextChunk, len(texts))
			for i, text := range texts {
				out[i] = []common.TextChunk{{Text: text, EndIndex: len(text), TokenCount: i + 1}}
			}

			return out, nil
		},
	})

	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	chunks, err := c.Chunker().ChunkToken(t.Context(), &chunker.TokenRequest{
		Texts:     []string{"hello world", "second text"},
		Tokenizer: chunker.TokenizerGPT2,
		ChunkSize: new(128),
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

	c, err := rootpuller.New(srv.URL, rootpuller.WithToken("test-token"))
	if err != nil {
		t.Fatal(err)
	}

	chk := c.Chunker().WithDeployment("cloudrun")
	if _, err := chk.ChunkToken(t.Context(), &chunker.TokenRequest{Texts: []string{"x"}}); err != nil {
		t.Fatal(err)
	}

	h := <-headers
	if got := h.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
	}

	if got := h.Get("Rootpuller-Deployment"); got != "cloudrun" {
		t.Errorf("rootpuller-deployment = %q, want cloudrun", got)
	}

	// Per-call context overrides beat the service-client default.
	ctx := rootpuller.ContextWithDeployment(t.Context(), "local")
	if _, err := chk.ChunkToken(ctx, &chunker.TokenRequest{Texts: []string{"x"}}); err != nil {
		t.Fatal(err)
	}

	h = <-headers
	if got := h.Get("Rootpuller-Deployment"); got != "local" {
		t.Errorf("override rootpuller-deployment = %q, want local", got)
	}

	// The derived client left the original untouched.
	if _, err := c.Chunker().ChunkToken(t.Context(), &chunker.TokenRequest{Texts: []string{"x"}}); err != nil {
		t.Fatal(err)
	}

	h = <-headers
	if got := h.Get("Rootpuller-Deployment"); got != "" {
		t.Errorf("base client rootpuller-deployment = %q, want unset", got)
	}
}

func TestNoAuthHeaderWithoutTokenSource(t *testing.T) {
	t.Parallel()

	srv, headers := newHeaderCapturingServer(t)

	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Chunker().ChunkToken(t.Context(), &chunker.TokenRequest{Texts: []string{"x"}}); err != nil {
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

	c, err := rootpuller.New(srv.URL, rootpuller.WithTokenSource(failingTokenSource{}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Chunker().ChunkToken(t.Context(), &chunker.TokenRequest{Texts: []string{"x"}})
	if !errors.Is(err, apierror.ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}

	if ae, ok := errors.AsType[*apierror.Error](err); !ok || ae.Code != connect.CodeUnauthenticated {
		t.Fatalf("err = %#v, want *apierror.Error with CodeUnauthenticated", err)
	}
}

func TestServerErrorWithRetryInfo(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Chunker{
		ChunkFunc: func(string, []string) ([][]common.TextChunk, error) {
			cerr := connect.NewError(connect.CodeResourceExhausted, errDeploymentSaturated)
			if detail, derr := connect.NewErrorDetail(&errdetails.RetryInfo{
				RetryDelay: durationpb.New(1500 * time.Millisecond),
			}); derr == nil {
				cerr.AddDetail(detail)
			}

			return nil, cerr
		},
	})

	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Chunker().ChunkToken(t.Context(), &chunker.TokenRequest{Texts: []string{"x"}})
	if !errors.Is(err, apierror.ErrResourceExhausted) {
		t.Fatalf("err = %v, want ErrResourceExhausted", err)
	}

	ae, ok := errors.AsType[*apierror.Error](err)
	if !ok {
		t.Fatalf("err = %#v, want *apierror.Error", err)
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
	c, err := rootpuller.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Chunker().ChunkToken(t.Context(), &chunker.TokenRequest{
		Texts:     []string{"x"},
		Tokenizer: chunker.Tokenizer("bogus"),
	})
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestAllMethodsDispatch(t *testing.T) {
	t.Parallel()

	var methods []string

	srv := rootpullertest.NewServer(t, &rootpullertest.Chunker{
		ChunkFunc: func(method string, texts []string) ([][]common.TextChunk, error) {
			methods = append(methods, method)

			return make([][]common.TextChunk, len(texts)), nil
		},
	})

	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	texts := []string{"x"}

	calls := []struct {
		name string
		call func() error
	}{
		{"ChunkSemantic", func() error {
			_, e := c.Chunker().ChunkSemantic(ctx, &chunker.SemanticRequest{Texts: texts})

			return e
		}},
		{"ChunkToken", func() error {
			_, e := c.Chunker().ChunkToken(ctx, &chunker.TokenRequest{Texts: texts})

			return e
		}},
		{"ChunkSentence", func() error {
			_, e := c.Chunker().ChunkSentence(ctx, &chunker.SentenceRequest{Texts: texts})

			return e
		}},
		{"ChunkCode", func() error {
			_, e := c.Chunker().ChunkCode(ctx, &chunker.CodeRequest{Texts: texts})

			return e
		}},
		{"ChunkRecursive", func() error {
			_, e := c.Chunker().ChunkRecursive(ctx, &chunker.RecursiveRequest{Texts: texts})

			return e
		}},
		{"ChunkLate", func() error {
			_, e := c.Chunker().ChunkLate(ctx, &chunker.LateRequest{Texts: texts})

			return e
		}},
		{"ChunkNeural", func() error {
			_, e := c.Chunker().ChunkNeural(ctx, &chunker.NeuralRequest{Texts: texts})

			return e
		}},
		{"ChunkSlumber", func() error {
			_, e := c.Chunker().ChunkSlumber(ctx, &chunker.SlumberRequest{Texts: texts})

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
