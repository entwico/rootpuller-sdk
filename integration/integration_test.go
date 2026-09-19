//go:build integration

// Package integration smoke-tests the SDK against a live rootpuller-api:
//
//	ROOTPULLER_ADDR=http://localhost:8755 go test -tags integration ./integration
//
// ROOTPULLER_TOKEN adds a bearer token when the server has auth enabled.
// ESCRIBA_RECORDING names an audio file for the recording test, which is skipped
// without it; ESCRIBA_SPEAKERS=channel|diarization adds speaker labels.
package integration

import (
	"fmt"
	"os"
	"testing"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/chunker"
	"github.com/entwico/rootpuller-sdk/embedding"
	"github.com/entwico/rootpuller-sdk/escriba"
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

func TestEscribaTranscribeRecording(t *testing.T) {
	path := os.Getenv("ESCRIBA_RECORDING")
	if path == "" {
		t.Skip("ESCRIBA_RECORDING not set")
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	opts := &escriba.RecordingOptions{
		OnProgress: func(p escriba.RecordingProgress) { t.Logf("%s %.0f%%", p.Stage, p.Percentage) },
	}
	if method := os.Getenv("ESCRIBA_SPEAKERS"); method != "" {
		opts.Speakers = &escriba.SpeakerOptions{Method: escriba.SpeakerMethod(method)}
	}

	recording, err := escriba.NewService(newSDK(t)).TranscribeRecording(t.Context(),
		rootpullersdk.Upload{Name: path, Content: file}, opts)
	if err != nil {
		t.Fatalf("TranscribeRecording: %v", err)
	}

	if len(recording.Segments) == 0 || recording.Text == "" {
		t.Fatalf("got an empty transcript for %s", path)
	}

	if opts.Speakers != nil && len(recording.Speakers) == 0 {
		t.Error("asked for speaker labels, got no speakers")
	}

	for _, turn := range recording.Turns() {
		who := "-"
		if turn.Speaker != nil {
			who = fmt.Sprintf("Speaker %d", *turn.Speaker+1)
		}

		t.Logf("[%s] %s: %s", turn.Start, who, turn.Text)
	}
}
