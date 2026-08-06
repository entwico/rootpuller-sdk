//go:build integration

// Package integration smoke-tests the SDK against a live rootpuller-api:
//
//	ROOTPULLER_ADDR=http://localhost:8755 go test -tags integration ./integration
//
// ROOTPULLER_TOKEN adds a bearer token when the server has auth enabled.
package integration

import (
	"os"
	"testing"

	rootpuller "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/chunker"
)

func newClient(t *testing.T) *rootpuller.Client {
	t.Helper()
	addr := os.Getenv("ROOTPULLER_ADDR")
	if addr == "" {
		t.Skip("ROOTPULLER_ADDR not set")
	}
	var opts []rootpuller.Option
	if token := os.Getenv("ROOTPULLER_TOKEN"); token != "" {
		opts = append(opts, rootpuller.WithToken(token))
	}
	c, err := rootpuller.New(addr, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestChunkToken(t *testing.T) {
	c := newClient(t)
	chunks, err := c.Chunker().ChunkToken(t.Context(), &chunker.TokenRequest{
		Texts: []string{"The quick brown fox jumps over the lazy dog. " + "It was a bright cold day in April."},
		// Character tokenizer needs no model download on the worker.
		Tokenizer: chunker.TokenizerCharacter,
		ChunkSize: rootpuller.Ptr(16),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || len(chunks[0]) == 0 {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
	t.Logf("got %d chunks", len(chunks[0]))
}

func TestEmbeddingListModels(t *testing.T) {
	c := newClient(t)
	models, err := c.Embedding().ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("server offers %d embedding models", len(models))
}
