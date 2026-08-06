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

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/chunker"
	"github.com/entwico/rootpuller-sdk/embedding"
)

func newSDK(t *testing.T) *rootpullersdk.Client {
	t.Helper()

	addr := os.Getenv("ROOTPULLER_ADDR")
	if addr == "" {
		t.Skip("ROOTPULLER_ADDR not set")
	}

	var opts []rootpullersdk.Option
	if token := os.Getenv("ROOTPULLER_TOKEN"); token != "" {
		opts = append(opts, rootpullersdk.WithToken(token))
	}

	sdk, err := rootpullersdk.New(addr, opts...)
	if err != nil {
		t.Fatal(err)
	}

	return sdk
}

func TestChunkToken(t *testing.T) {
	svc := chunker.NewService(newSDK(t))

	chunks, err := svc.ChunkToken(t.Context(),
		[]string{"The quick brown fox jumps over the lazy dog. It was a bright cold day in April."},
		&chunker.TokenOptions{
			// Character tokenizer needs no model download on the worker.
			Tokenizer: chunker.TokenizerCharacter,
			ChunkSize: 16,
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
	svc := embedding.NewService(newSDK(t))

	models, err := svc.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("server offers %d embedding models", len(models))
}
