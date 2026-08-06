package rootpullersdk_test

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	rerankpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank/rerankconnect"
	"github.com/entwico/rootpuller-sdk/rerank"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

// trailerRerank responds with rate-limit trailers like the real server.
type trailerRerank struct {
	rerankconnect.UnimplementedRerankServiceHandler
}

func (h *trailerRerank) Rerank(_ context.Context, _ *connect.Request[rerankpb.RerankRequest]) (*connect.Response[rerankpb.RerankResponse], error) {
	resp := connect.NewResponse(&rerankpb.RerankResponse{})
	resp.Trailer().Set("x-ratelimit-limit", "8")
	resp.Trailer().Set("x-ratelimit-remaining", "3")

	return resp, nil
}

func TestCaptureTrailers(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.Handle(rerankconnect.NewRerankServiceHandler(&trailerRerank{}))
	srv := rootpullertest.NewServerWithMux(t, mux)

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	svc := rerank.NewService(sdk)

	ctx, trailers := rootpullersdk.CaptureTrailers(t.Context())
	if _, err := svc.Rerank(ctx, "q", []string{"d"}, nil); err != nil {
		t.Fatal(err)
	}

	if got := trailers.Get("x-ratelimit-limit"); got != "8" {
		t.Errorf("x-ratelimit-limit = %q, want 8", got)
	}

	if got := trailers.Get("X-Ratelimit-Remaining"); got != "3" {
		t.Errorf("x-ratelimit-remaining (canonical key) = %q, want 3", got)
	}

	// Without opting in, calls are unaffected.
	if _, err := svc.Rerank(t.Context(), "q", []string{"d"}, nil); err != nil {
		t.Fatal(err)
	}
}
