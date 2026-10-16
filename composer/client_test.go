package composer_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/composer"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

var errQueueFull = errors.New("the queue is full")

func newService(t *testing.T, baseURL string) *composer.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return composer.NewService(sdk)
}

func TestComposeSongMultiCandidateDemux(t *testing.T) {
	t.Parallel()

	// Two takes over one response arm: the client must demux the chunk
	// stream by file name and attach each take's audio to its metadata.
	// 3 MiB each → 2 chunks per file.
	payload0 := bytes.Repeat([]byte{0x01}, 3<<20)
	payload1 := bytes.Repeat([]byte{0x02}, 3<<20)

	var gotOpts composer.SongOptions

	srv := rootpullertest.NewServer(t, &rootpullertest.Composer{
		ComposeFunc: func(_, _ string, opts composer.SongOptions) (*composer.Song, error) {
			gotOpts = opts

			return &composer.Song{
				Seed:           10,
				Cot:            composer.CotModeFull,
				ABC:            "X:1\nscore",
				Decoder:        composer.DecoderLegacy,
				ProcessingTime: 90 * time.Second,
				Candidates: []composer.Candidate{
					{Index: 0, Seed: 10, Duration: 3 * time.Second,
						Audio: rootpullersdk.File{Name: "song_0.flac", MIMEType: "audio/flac", Data: payload0}},
					{Index: 1, Seed: 11, Duration: 4 * time.Second, Truncated: true,
						Audio: rootpullersdk.File{Name: "song_1.flac", MIMEType: "audio/flac", Data: payload1}},
				},
			}, nil
		},
		Progress: []composer.Progress{
			{Stage: composer.StagePlanning, Completed: 5},
			{Stage: composer.StageGenerating, Completed: 1, Candidate: new(1)},
		},
	})

	var stages []composer.Stage

	var candidateSeen bool

	song, err := newService(t, srv.URL).ComposeSong(t.Context(), "jazz", "[verse]\nla", &composer.SongOptions{
		Seed:             new(int64(10)),
		Candidates:       2,
		Decoder:          composer.DecoderLegacy,
		SemanticSampling: &composer.Sampling{MaxTokens: new(12000)},
		OnProgress: func(p composer.Progress) {
			stages = append(stages, p.Stage)
			if p.Candidate != nil && *p.Candidate == 1 {
				candidateSeen = true
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotOpts.Decoder != composer.DecoderLegacy || gotOpts.Candidates != 2 {
		t.Errorf("server saw decoder %q, candidates %d", gotOpts.Decoder, gotOpts.Candidates)
	}

	if gotOpts.SemanticSampling == nil || *gotOpts.SemanticSampling.MaxTokens != 12000 {
		t.Error("semantic sampling override did not reach the server")
	}

	if len(song.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(song.Candidates))
	}

	if !bytes.Equal(song.Candidates[0].Audio.Data, payload0) || !bytes.Equal(song.Candidates[1].Audio.Data, payload1) {
		t.Error("candidate audio was not demuxed correctly")
	}

	if !song.Candidates[1].Truncated || song.Candidates[1].Duration != 4*time.Second {
		t.Errorf("candidate metadata lost: %+v", song.Candidates[1])
	}

	if song.Decoder != composer.DecoderLegacy || song.ProcessingTime != 90*time.Second {
		t.Errorf("song metadata lost: %+v", song)
	}

	if len(stages) != 2 || !candidateSeen {
		t.Errorf("progress not forwarded: stages %v, candidate seen %v", stages, candidateSeen)
	}
}

func TestPlanReturnsTheEditableScore(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Composer{})

	plan, err := newService(t, srv.URL).Plan(t.Context(), "piano pop", "", &composer.PlanOptions{
		Seed:         new(int64(7)),
		Instrumental: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Seed != 7 {
		t.Errorf("got seed %d, want 7", plan.Seed)
	}

	if plan.ABC == "" || plan.ABCOriginal == "" {
		t.Errorf("instrumental plan must carry both scores: %+v", plan)
	}
}

func TestEmptyLyricsNeedInstrumental(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Composer{})

	_, err := newService(t, srv.URL).Plan(t.Context(), "piano pop", "", nil)
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("got %v, want invalid argument", err)
	}

	_, err = newService(t, srv.URL).ComposeSong(t.Context(), "piano pop", "", nil)
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("got %v, want invalid argument", err)
	}
}

func TestAScoreNeedsAPlanningMode(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Composer{})

	_, err := newService(t, srv.URL).ComposeSong(t.Context(), "jazz", "la", &composer.SongOptions{
		Cot: composer.CotModeOff,
		ABC: "X:1\nscore",
	})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("got %v, want invalid argument", err)
	}
}

func TestTranscribeUploadsAndReturnsTheScore(t *testing.T) {
	t.Parallel()

	// 3 MiB → the upload must arrive reassembled from 2 chunks.
	payload := bytes.Repeat([]byte{0x03}, 3<<20)

	var (
		gotAudio      rootpullersdk.File
		gotMelodyOnly bool
	)

	srv := rootpullertest.NewServer(t, &rootpullertest.Composer{
		TranscribeFunc: func(audio rootpullersdk.File, melodyOnly bool) (*composer.Transcription, error) {
			gotAudio, gotMelodyOnly = audio, melodyOnly

			return &composer.Transcription{ABC: "X:1\ntranscribed", MelodyOnly: melodyOnly}, nil
		},
	})

	result, err := newService(t, srv.URL).Transcribe(t.Context(),
		rootpullersdk.UploadBytes("clip.mp3", "audio/mpeg", payload),
		&composer.TranscribeOptions{MelodyOnly: new(false)})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(gotAudio.Data, payload) || gotAudio.Name != "clip.mp3" {
		t.Error("upload was not reassembled")
	}

	if gotMelodyOnly {
		t.Error("explicit melody_only=false did not reach the server")
	}

	if result.ABC != "X:1\ntranscribed" {
		t.Errorf("got %q", result.ABC)
	}
}

func TestTranscribeDefaultsToMelodyOnly(t *testing.T) {
	t.Parallel()

	var gotMelodyOnly bool

	srv := rootpullertest.NewServer(t, &rootpullertest.Composer{
		TranscribeFunc: func(_ rootpullersdk.File, melodyOnly bool) (*composer.Transcription, error) {
			gotMelodyOnly = melodyOnly

			return &composer.Transcription{ABC: "X:1", MelodyOnly: melodyOnly}, nil
		},
	})

	if _, err := newService(t, srv.URL).Transcribe(t.Context(),
		rootpullersdk.UploadBytes("clip.wav", "audio/wav", []byte{1}), nil); err != nil {
		t.Fatal(err)
	}

	if !gotMelodyOnly {
		t.Error("unset melody_only must default to true")
	}
}

func TestGetCapabilities(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Composer{})

	caps, err := newService(t, srv.URL).GetCapabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if caps.MaxCandidates != 8 || !caps.Transcription || caps.SampleRate != 48000 {
		t.Errorf("unexpected canned capabilities: %+v", caps)
	}

	if caps.SemanticSamplingDefaults.MaxTokens != 9000 {
		t.Errorf("sampling defaults lost: %+v", caps.SemanticSamplingDefaults)
	}
}

func TestComposeErrorPropagates(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Composer{
		ComposeFunc: func(string, string, composer.SongOptions) (*composer.Song, error) {
			return nil, connect.NewError(connect.CodeResourceExhausted, errQueueFull)
		},
	})

	_, err := newService(t, srv.URL).ComposeSong(t.Context(), "jazz", "la", nil)
	if !errors.Is(err, rootpullersdk.ErrResourceExhausted) {
		t.Fatalf("got %v, want resource exhausted", err)
	}
}
