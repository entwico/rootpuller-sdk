package rerank_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	rerankpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank/rerankconnect"
	"github.com/entwico/rootpuller-sdk/rerank"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

func newService(t *testing.T, baseURL string, opts ...rerank.Option) *rerank.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return rerank.NewService(sdk, opts...)
}

func TestRerankRoundTrip(t *testing.T) {
	t.Parallel()

	var (
		gotQuery     string
		gotDocuments []string
	)

	srv := rootpullertest.NewServer(t, &rootpullertest.Rerank{
		RerankFunc: func(query string, documents []string) ([]rerank.Result, error) {
			gotQuery = query
			gotDocuments = documents

			return []rerank.Result{
				{Index: 1, RelevanceScore: 0.9, Document: documents[1]},
				{Index: 0, RelevanceScore: 0.2},
			}, nil
		},
	})

	svc := newService(t, srv.URL)

	resp, err := svc.Rerank(t.Context(), "what is chunking",
		[]string{"unrelated", "chunking splits text"},
		&rerank.Options{
			Model:            "BAAI/bge-reranker-v2-m3",
			InferenceBackend: rerank.InferenceBackendFlagReranker,
		})
	if err != nil {
		t.Fatal(err)
	}

	if gotQuery != "what is chunking" {
		t.Errorf("server saw query %q, want %q", gotQuery, "what is chunking")
	}

	if len(gotDocuments) != 2 || gotDocuments[0] != "unrelated" {
		t.Errorf("server saw documents %v", gotDocuments)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}

	if resp.Results[0].Index != 1 || resp.Results[0].RelevanceScore != 0.9 || resp.Results[0].Document != "chunking splits text" {
		t.Errorf("unexpected first result: %+v", resp.Results[0])
	}

	if resp.Results[1].Index != 0 || resp.Results[1].RelevanceScore != 0.2 {
		t.Errorf("unexpected second result: %+v", resp.Results[1])
	}
	// The fake echoes the request model, so the full ModelRef (including
	// the backend enum) must survive the round trip.
	want := rerank.ModelRef{ModelID: "BAAI/bge-reranker-v2-m3", InferenceBackend: rerank.InferenceBackendFlagReranker}
	if resp.Model != want {
		t.Errorf("Model = %+v, want %+v", resp.Model, want)
	}
}

// capturingRerank records the proto request of each Rerank call.
type capturingRerank struct {
	rerankconnect.UnimplementedRerankServiceHandler

	requests chan *rerankpb.RerankRequest
}

func (h *capturingRerank) Rerank(_ context.Context, req *connect.Request[rerankpb.RerankRequest]) (*connect.Response[rerankpb.RerankResponse], error) {
	h.requests <- req.Msg

	return connect.NewResponse(&rerankpb.RerankResponse{}), nil
}

func newCapturingServer(t *testing.T) (*rootpullertest.Server, chan *rerankpb.RerankRequest) {
	t.Helper()

	handler := &capturingRerank{requests: make(chan *rerankpb.RerankRequest, 1)}
	mux := http.NewServeMux()
	mux.Handle(rerankconnect.NewRerankServiceHandler(handler))

	return rootpullertest.NewServerWithMux(t, mux), handler.requests
}

func TestRerankOptionsRoundTrip(t *testing.T) {
	t.Parallel()

	srv, requests := newCapturingServer(t)
	svc := newService(t, srv.URL)

	// All optional fields set: the proto options message must carry them.
	_, err := svc.Rerank(t.Context(), "q", []string{"d"}, &rerank.Options{
		Model:           "m",
		TopN:            5,
		MaxTokens:       1024,
		ReturnDocuments: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg := <-requests

	opts := msg.GetOptions()
	if opts == nil {
		t.Fatal("Options = nil, want populated message")
	}

	if opts.TopN == nil || opts.GetTopN() != 5 {
		t.Errorf("TopN = %v, want 5", opts.TopN)
	}

	if opts.MaxTokens == nil || opts.GetMaxTokens() != 1024 {
		t.Errorf("MaxTokens = %v, want 1024", opts.MaxTokens)
	}

	if !opts.GetReturnDocuments() {
		t.Error("ReturnDocuments = false, want true")
	}

	// Nil options: the proto options message must be omitted entirely.
	if _, err := svc.Rerank(t.Context(), "q", []string{"d"}, nil); err != nil {
		t.Fatal(err)
	}

	msg = <-requests
	if msg.GetOptions() != nil {
		t.Errorf("Options = %v, want nil when no optional field is set", msg.GetOptions())
	}
}

func TestServiceDefaults(t *testing.T) {
	t.Parallel()

	srv, requests := newCapturingServer(t)
	svc := newService(t, srv.URL,
		rerank.WithDefaultModel("BAAI/bge-reranker-v2-m3"))

	// The construction-time default model applies when Options leave it
	// empty...
	if _, err := svc.Rerank(t.Context(), "q", []string{"d"}, nil); err != nil {
		t.Fatal(err)
	}

	if got := (<-requests).GetModel().GetModelId(); got != "BAAI/bge-reranker-v2-m3" {
		t.Errorf("model = %q, want service default", got)
	}

	// ...and a per-call model wins over the default.
	if _, err := svc.Rerank(t.Context(), "q", []string{"d"}, &rerank.Options{Model: "other"}); err != nil {
		t.Fatal(err)
	}

	if got := (<-requests).GetModel().GetModelId(); got != "other" {
		t.Errorf("model = %q, want per-call override", got)
	}
}

func TestListModelsRoundTrip(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Rerank{
		Models: []rerank.ModelInfo{
			{
				Model: rerank.ModelRef{
					ModelID:          "Qwen/Qwen3-Reranker-0.6B",
					InferenceBackend: rerank.InferenceBackendSentenceTransformers,
				},
				MaxTokens:   32768,
				Loaded:      true,
				Description: "Qwen3 reranker",
			},
			{
				Model:  rerank.ModelRef{ModelID: "BAAI/bge-reranker-v2-m3"},
				Loaded: false,
			},
		},
	})

	svc := newService(t, srv.URL)

	models, err := svc.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}

	want := rerank.ModelInfo{
		Model: rerank.ModelRef{
			ModelID:          "Qwen/Qwen3-Reranker-0.6B",
			InferenceBackend: rerank.InferenceBackendSentenceTransformers,
		},
		MaxTokens:   32768,
		Loaded:      true,
		Description: "Qwen3 reranker",
	}
	if models[0] != want {
		t.Errorf("models[0] = %+v, want %+v", models[0], want)
	}

	if models[1].Model.InferenceBackend != rerank.InferenceBackendDefault || models[1].Loaded {
		t.Errorf("models[1] = %+v", models[1])
	}
}

func TestInvalidBackendFailsLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	_, err := svc.Rerank(t.Context(), "q", []string{"d"}, &rerank.Options{
		Model:            "m",
		InferenceBackend: rerank.InferenceBackend("bogus"),
	})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
