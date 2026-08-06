package embedding_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/embedding"
	embeddingpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/embedding"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/embedding/embeddingconnect"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

var errModelNotLoaded = errors.New("model not loaded")

func newService(t *testing.T, baseURL string, opts ...embedding.Option) *embedding.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return embedding.NewService(sdk, opts...)
}

func newFakeService(t *testing.T, fake *rootpullertest.Embedding) *embedding.Service {
	t.Helper()

	return newService(t, rootpullertest.NewServer(t, fake).URL)
}

func TestEmbed(t *testing.T) {
	t.Parallel()

	svc := newFakeService(t, &rootpullertest.Embedding{
		EmbedFunc: func(inputs []embedding.Input) ([][]float32, error) {
			out := make([][]float32, len(inputs))
			for i, in := range inputs {
				out[i] = []float32{float32(len(in.Content)), float32(i)}
			}

			return out, nil
		},
	})

	resp, err := svc.Embed(t.Context(), embedding.Text("hello", "world!!"), &embedding.Options{
		Model: embedding.ModelRef{Backend: embedding.BackendLocal, ModelID: "test-model"},
		Mode:  embedding.ModeDense,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.Embeddings) != 2 {
		t.Fatalf("got %d embeddings, want 2", len(resp.Embeddings))
	}

	if resp.Embeddings[0].Kind != embedding.ModeDense {
		t.Errorf("Kind = %q, want dense", resp.Embeddings[0].Kind)
	}

	if got := resp.Embeddings[1].Dense[0]; got != 7 {
		t.Errorf("second vector first value = %v, want 7 (len of input)", got)
	}

	if resp.Model.ModelID != "test-model" || resp.Model.Backend != embedding.BackendLocal {
		t.Errorf("model round trip failed: %+v", resp.Model)
	}

	if resp.DenseDimension != 2 {
		t.Errorf("DenseDimension = %d, want 2", resp.DenseDimension)
	}
}

func TestEmbedStream(t *testing.T) {
	t.Parallel()

	svc := newFakeService(t, &rootpullertest.Embedding{})

	got := make([]embedding.StreamResult, 0, 3)

	for result, err := range svc.EmbedStream(t.Context(), embedding.Text("a", "b", "c"), nil) {
		if err != nil {
			t.Fatal(err)
		}

		got = append(got, result)
	}

	if len(got) != 3 {
		t.Fatalf("got %d stream results, want 3", len(got))
	}

	for i, r := range got {
		if r.Embedding.Kind != embedding.ModeDense || len(r.Embedding.Dense) != 4 {
			t.Errorf("result %d: unexpected embedding %+v", i, r.Embedding)
		}
	}
}

func TestEmbedStreamEarlyBreak(t *testing.T) {
	t.Parallel()

	svc := newFakeService(t, &rootpullertest.Embedding{})

	count := 0

	for _, err := range svc.EmbedStream(t.Context(), embedding.Text("a", "b", "c", "d"), nil) {
		if err != nil {
			t.Fatal(err)
		}

		count++
		if count == 2 {
			break // must close the stream cleanly, no goroutine leak
		}
	}

	if count != 2 {
		t.Fatalf("iterated %d times, want 2", count)
	}
}

func TestEmbedStreamServerError(t *testing.T) {
	t.Parallel()

	svc := newFakeService(t, &rootpullertest.Embedding{
		EmbedFunc: func([]embedding.Input) ([][]float32, error) {
			return nil, errModelNotLoaded
		},
	})

	var lastErr error
	for _, err := range svc.EmbedStream(t.Context(), embedding.Text("a"), nil) {
		lastErr = err
	}

	if lastErr == nil {
		t.Fatal("want an error from the stream")
	}

	if _, ok := errors.AsType[*rootpullersdk.Error](lastErr); !ok {
		t.Fatalf("err = %#v, want *rootpullersdk.Error", lastErr)
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()

	svc := newFakeService(t, &rootpullertest.Embedding{
		Models: []embedding.ModelInfo{
			{Model: embedding.ModelRef{ModelID: "bge-m3"}, DenseDimension: 1024, MaxTokens: 8192, Loaded: true, Description: "BGE-M3"},
		},
	})

	models, err := svc.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(models) != 1 || models[0].Model.ModelID != "bge-m3" || models[0].DenseDimension != 1024 {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestEmbedInvalidEnumFailsLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	_, err := svc.Embed(t.Context(), embedding.Text("x"), &embedding.Options{
		Mode: embedding.Mode("bogus"),
	})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	// The same local validation guards the stream variant.
	for _, serr := range svc.EmbedStream(t.Context(), nil, &embedding.Options{Mode: embedding.Mode("bogus")}) {
		err = serr
	}

	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("stream err = %v, want ErrInvalidArgument", err)
	}
}

// capturingEmbedding records the proto request of each Embed call.
type capturingEmbedding struct {
	embeddingconnect.UnimplementedVectorEmbeddingServiceHandler

	requests chan *embeddingpb.EmbedRequest
}

func (h *capturingEmbedding) Embed(_ context.Context, req *connect.Request[embeddingpb.EmbedRequest]) (*connect.Response[embeddingpb.EmbedResponse], error) {
	h.requests <- req.Msg

	return connect.NewResponse(&embeddingpb.EmbedResponse{}), nil
}

func newCapturingServer(t *testing.T) (*rootpullertest.Server, chan *embeddingpb.EmbedRequest) {
	t.Helper()

	handler := &capturingEmbedding{requests: make(chan *embeddingpb.EmbedRequest, 1)}
	mux := http.NewServeMux()
	mux.Handle(embeddingconnect.NewVectorEmbeddingServiceHandler(handler))

	return rootpullertest.NewServerWithMux(t, mux), handler.requests
}

func TestEmbedOptionsRoundTrip(t *testing.T) {
	t.Parallel()

	srv, requests := newCapturingServer(t)
	svc := newService(t, srv.URL)

	// All optional fields set: the proto options message must carry them.
	_, err := svc.Embed(t.Context(), embedding.Text("x"), &embedding.Options{
		Model:      embedding.ModelRef{Backend: embedding.BackendLocal, ModelID: "m"},
		Mode:       embedding.ModeDense,
		Task:       embedding.TaskRetrievalQuery,
		Dimensions: 256,
		MaxTokens:  512,
		Normalize:  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg := <-requests

	opts := msg.GetOptions()
	if opts == nil {
		t.Fatal("Options = nil, want populated message")
	}

	if opts.Dimensions == nil || opts.GetDimensions() != 256 {
		t.Errorf("Dimensions = %v, want 256", opts.Dimensions)
	}

	if opts.MaxTokens == nil || opts.GetMaxTokens() != 512 {
		t.Errorf("MaxTokens = %v, want 512", opts.MaxTokens)
	}

	if !opts.GetNormalize() {
		t.Error("Normalize = false, want true")
	}

	if msg.GetTask() != embeddingpb.EmbeddingTask_EMBEDDING_TASK_RETRIEVAL_QUERY {
		t.Errorf("Task = %v, want retrieval query", msg.GetTask())
	}

	// Nil options: the proto options message must be omitted entirely.
	if _, err := svc.Embed(t.Context(), embedding.Text("x"), nil); err != nil {
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
		embedding.WithDefaultModel(embedding.ModelRef{Backend: embedding.BackendLocal, ModelID: "bge-m3"}))

	// The construction-time default model applies when Options leave it
	// empty...
	if _, err := svc.Embed(t.Context(), embedding.Text("x"), nil); err != nil {
		t.Fatal(err)
	}

	msg := <-requests
	if msg.GetModel().GetModelId() != "bge-m3" || msg.GetModel().GetBackend() != embeddingpb.ModelBackend_MODEL_BACKEND_LOCAL {
		t.Errorf("model = %v, want service default", msg.GetModel())
	}

	// ...and a per-call model wins over the default.
	if _, err := svc.Embed(t.Context(), embedding.Text("x"), &embedding.Options{
		Model: embedding.ModelRef{ModelID: "other"},
	}); err != nil {
		t.Fatal(err)
	}

	if got := (<-requests).GetModel().GetModelId(); got != "other" {
		t.Errorf("model = %q, want per-call override", got)
	}
}
