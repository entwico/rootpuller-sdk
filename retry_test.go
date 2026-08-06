package rootpullersdk_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	rerankpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank/rerankconnect"
	"github.com/entwico/rootpuller-sdk/rerank"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

// flakyRerank fails the first failures calls with code, then succeeds.
type flakyRerank struct {
	rerankconnect.UnimplementedRerankServiceHandler

	calls    atomic.Int64
	failures int64
	code     connect.Code
}

var errFlaky = errors.New("transient hiccup")

func (h *flakyRerank) Rerank(_ context.Context, _ *connect.Request[rerankpb.RerankRequest]) (*connect.Response[rerankpb.RerankResponse], error) {
	if h.calls.Add(1) <= h.failures {
		return nil, connect.NewError(h.code, errFlaky)
	}

	return connect.NewResponse(&rerankpb.RerankResponse{}), nil
}

func newFlakyService(t *testing.T, handler *flakyRerank, opts ...rootpullersdk.Option) *rerank.Service {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(rerankconnect.NewRerankServiceHandler(handler))
	srv := rootpullertest.NewServerWithMux(t, mux)

	sdk, err := rootpullersdk.New(srv.URL, opts...)
	if err != nil {
		t.Fatal(err)
	}

	return rerank.NewService(sdk)
}

func TestWithRetryRecoversTransientFailures(t *testing.T) {
	t.Parallel()

	handler := &flakyRerank{failures: 2, code: connect.CodeUnavailable}
	svc := newFlakyService(t, handler, rootpullersdk.WithRetry(rootpullersdk.RetryOptions{
		InitialInterval: time.Millisecond,
	}))

	if _, err := svc.Rerank(t.Context(), "q", []string{"d"}, nil); err != nil {
		t.Fatalf("want success after retries, got %v", err)
	}

	if got := handler.calls.Load(); got != 3 {
		t.Errorf("server saw %d calls, want 3 (2 failures + success)", got)
	}
}

func TestWithRetrySkipsPermanentFailures(t *testing.T) {
	t.Parallel()

	handler := &flakyRerank{failures: 10, code: connect.CodeInvalidArgument}
	svc := newFlakyService(t, handler, rootpullersdk.WithRetry(rootpullersdk.RetryOptions{
		InitialInterval: time.Millisecond,
	}))

	_, err := svc.Rerank(t.Context(), "q", []string{"d"}, nil)
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}

	if got := handler.calls.Load(); got != 1 {
		t.Errorf("server saw %d calls, want 1 (no retry on permanent errors)", got)
	}
}

func TestWithRetryGivesUpAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	handler := &flakyRerank{failures: 100, code: connect.CodeUnavailable}
	svc := newFlakyService(t, handler, rootpullersdk.WithRetry(rootpullersdk.RetryOptions{
		MaxAttempts:     3,
		InitialInterval: time.Millisecond,
	}))

	_, err := svc.Rerank(t.Context(), "q", []string{"d"}, nil)
	if !errors.Is(err, rootpullersdk.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}

	if got := handler.calls.Load(); got != 3 {
		t.Errorf("server saw %d calls, want 3 (MaxAttempts)", got)
	}
}

func TestNoRetryWithoutOption(t *testing.T) {
	t.Parallel()

	handler := &flakyRerank{failures: 1, code: connect.CodeUnavailable}
	svc := newFlakyService(t, handler)

	_, err := svc.Rerank(t.Context(), "q", []string{"d"}, nil)
	if !errors.Is(err, rootpullersdk.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable (no retry configured)", err)
	}

	if got := handler.calls.Load(); got != 1 {
		t.Errorf("server saw %d calls, want 1", got)
	}
}
