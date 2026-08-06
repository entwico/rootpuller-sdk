package rerank_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	rerankpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank/rerankconnect"
	"github.com/entwico/rootpuller-sdk/internal/transport"
	"github.com/entwico/rootpuller-sdk/rerank"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

// newClient dials baseURL the same way rootpuller.New does. The
// rootpuller.Client accessor for this service is wired separately, so the
// tests construct the service client directly from a transport.Core.
func newClient(t *testing.T, baseURL string) *rerank.Client {
	t.Helper()

	httpClient, err := transport.NewHTTPClient(baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	return rerank.NewFromCore(&transport.Core{
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		ClientOpts: []connect.ClientOption{connect.WithGRPC()},
	})
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

	c := newClient(t, srv.URL)

	resp, err := c.Rerank(t.Context(), &rerank.Request{
		Query:     "what is chunking",
		Documents: []string{"unrelated", "chunking splits text"},
		Model: rerank.ModelRef{
			ModelID:          "BAAI/bge-reranker-v2-m3",
			InferenceBackend: rerank.InferenceBackendFlagReranker,
		},
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

func TestRerankOptionsRoundTrip(t *testing.T) {
	t.Parallel()

	handler := &capturingRerank{requests: make(chan *rerankpb.RerankRequest, 1)}
	mux := http.NewServeMux()
	mux.Handle(rerankconnect.NewRerankServiceHandler(handler))
	srv := rootpullertest.NewServerWithMux(t, mux)
	c := newClient(t, srv.URL)

	// All optional fields set: the proto options message must carry them.
	_, err := c.Rerank(t.Context(), &rerank.Request{
		Query:           "q",
		Documents:       []string{"d"},
		Model:           rerank.ModelRef{ModelID: "m"},
		TopN:            new(5),
		MaxTokens:       new(1024),
		ReturnDocuments: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg := <-handler.requests

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

	// No optional fields set: the options message must be omitted.
	_, err = c.Rerank(t.Context(), &rerank.Request{
		Query:     "q",
		Documents: []string{"d"},
		Model:     rerank.ModelRef{ModelID: "m"},
	})
	if err != nil {
		t.Fatal(err)
	}

	msg = <-handler.requests
	if msg.GetOptions() != nil {
		t.Errorf("Options = %v, want nil when no optional field is set", msg.GetOptions())
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

	c := newClient(t, srv.URL)

	models, err := c.ListModels(t.Context())
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
	c := newClient(t, "http://127.0.0.1:1")

	_, err := c.Rerank(t.Context(), &rerank.Request{
		Query:     "q",
		Documents: []string{"d"},
		Model: rerank.ModelRef{
			ModelID:          "m",
			InferenceBackend: rerank.InferenceBackend("bogus"),
		},
	})
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
