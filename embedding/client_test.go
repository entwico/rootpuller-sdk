package embedding_test

import (
	"errors"
	"testing"

	rootpuller "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/embedding"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

var errModelNotLoaded = errors.New("model not loaded")

func newClient(t *testing.T, fake *rootpullertest.Embedding) *rootpuller.Client {
	t.Helper()
	srv := rootpullertest.NewServer(t, fake)

	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestEmbed(t *testing.T) {
	t.Parallel()

	c := newClient(t, &rootpullertest.Embedding{
		EmbedFunc: func(inputs []embedding.Input) ([][]float32, error) {
			out := make([][]float32, len(inputs))
			for i, in := range inputs {
				out[i] = []float32{float32(len(in.Content)), float32(i)}
			}

			return out, nil
		},
	})

	resp, err := c.Embedding().Embed(t.Context(), &embedding.Request{
		Inputs: embedding.Text("hello", "world!!"),
		Model:  embedding.ModelRef{Backend: embedding.BackendLocal, ModelID: "test-model"},
		Mode:   embedding.ModeDense,
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

	c := newClient(t, &rootpullertest.Embedding{})

	got := make([]embedding.StreamResult, 0, 3)

	for result, err := range c.Embedding().EmbedStream(t.Context(), &embedding.Request{
		Inputs: embedding.Text("a", "b", "c"),
	}) {
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

	c := newClient(t, &rootpullertest.Embedding{})

	count := 0

	for _, err := range c.Embedding().EmbedStream(t.Context(), &embedding.Request{
		Inputs: embedding.Text("a", "b", "c", "d"),
	}) {
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

	c := newClient(t, &rootpullertest.Embedding{
		EmbedFunc: func([]embedding.Input) ([][]float32, error) {
			return nil, errModelNotLoaded
		},
	})

	var lastErr error
	for _, err := range c.Embedding().EmbedStream(t.Context(), &embedding.Request{Inputs: embedding.Text("a")}) {
		lastErr = err
	}

	if lastErr == nil {
		t.Fatal("want an error from the stream")
	}

	if _, ok := errors.AsType[*apierror.Error](lastErr); !ok {
		t.Fatalf("err = %#v, want *apierror.Error", lastErr)
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()

	c := newClient(t, &rootpullertest.Embedding{
		Models: []embedding.ModelInfo{
			{Model: embedding.ModelRef{ModelID: "bge-m3"}, DenseDimension: 1024, MaxTokens: 8192, Loaded: true, Description: "BGE-M3"},
		},
	})

	models, err := c.Embedding().ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(models) != 1 || models[0].Model.ModelID != "bge-m3" || models[0].DenseDimension != 1024 {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestEmbedInvalidEnumFailsLocally(t *testing.T) {
	t.Parallel()

	c, err := rootpuller.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Embedding().Embed(t.Context(), &embedding.Request{
		Inputs: embedding.Text("x"),
		Mode:   embedding.Mode("bogus"),
	})
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	// The same local validation guards the stream variant.
	for _, serr := range c.Embedding().EmbedStream(t.Context(), &embedding.Request{Mode: embedding.Mode("bogus")}) {
		err = serr
	}

	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("stream err = %v, want ErrInvalidArgument", err)
	}
}
