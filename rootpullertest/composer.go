package rootpullertest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/composer"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	composerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/composer"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/composer/composerconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
)

// Composer is a facade-typed fake ComposerService. It enforces the wire
// protocol strictly, mirroring the real server's validation: a spec must be
// present with a non-empty style, lyrics may be empty only with instrumental,
// a score needs cot full or melody, and Transcribe requires the config frame
// first and exactly once, with chunks capped at 2 MiB.
//
// Nil hooks answer with canned results: a small native score, one FLAC take
// per requested candidate (seeds seed..seed+N-1), and permissive
// capabilities. Progress frames are streamed before every result, the way
// the real server interleaves them.
type Composer struct {
	// PlanFunc answers Plan. opts carries the decoded spec (callbacks
	// excluded).
	PlanFunc func(style, lyrics string, opts composer.PlanOptions) (*composer.Plan, error)

	// ComposeFunc answers ComposeSong. The returned Song's candidates are
	// streamed as its metadata and audio files, in order.
	ComposeFunc func(style, lyrics string, opts composer.SongOptions) (*composer.Song, error)

	// TranscribeFunc answers Transcribe with the reassembled upload.
	TranscribeFunc func(audio rootpullersdk.File, melodyOnly bool) (*composer.Transcription, error)

	// Capabilities is returned by GetCapabilities; nil synthesizes a
	// permissive default.
	Capabilities *composer.Capabilities

	// Progress frames streamed before each result.
	Progress []composer.Progress
}

func (f *Composer) register(mux *http.ServeMux) {
	mux.Handle(composerconnect.NewComposerServiceHandler(&composerHandler{fake: f}))
}

type composerHandler struct {
	composerconnect.UnimplementedComposerServiceHandler

	fake *Composer
}

// Composer-specific protocol-violation sentinels.
var (
	errEmptyStyle               = errors.New("empty style")
	errLyricsNeedInstrumental   = errors.New("empty lyrics without instrumental")
	errScoreWithCotOff          = errors.New("abc with cot off")
	errTooManyCandidates        = errors.New("candidates exceed the deployment cap")
	errNegativeCandidates       = errors.New("negative candidates")
	errMissingTranscriptionData = errors.New("missing audio chunks")
)

const cannedComposerScore = "X:1\nT:\nM:4/4\nL:1/32\nQ:1/4=96\n" +
	"V: Vocal clef=treble name=\"Vocal Melody\" snm=\"Vocal\"\n" +
	"V: Ins clef=treble name=\"Ins Melody\" snm=\"Inst.\"\nK:G\n% verse\n" +
	"V: Vocal\n\"G\"d8e8\"D7\"B8A8|\nV: Ins\nz32|\n"

const cannedComposerSeed = 831001

func validateSpec(spec *composerpb.SongSpec) error {
	if spec == nil {
		return invalidArgument(errMissingParams)
	}

	if spec.GetStyle() == "" {
		return invalidArgument(errEmptyStyle)
	}

	if spec.GetLyrics() == "" && !spec.GetInstrumental() {
		return invalidArgument(errLyricsNeedInstrumental)
	}

	if spec.Abc != nil && spec.GetCot() == composerpb.CotMode_COT_MODE_OFF {
		return invalidArgument(errScoreWithCotOff)
	}

	return nil
}

var composerCotFromProto = map[composerpb.CotMode]composer.CotMode{
	composerpb.CotMode_COT_MODE_UNSPECIFIED: composer.CotModeDefault,
	composerpb.CotMode_COT_MODE_FULL:        composer.CotModeFull,
	composerpb.CotMode_COT_MODE_MELODY:      composer.CotModeMelody,
	composerpb.CotMode_COT_MODE_OFF:         composer.CotModeOff,
}

var composerCotToProto = map[composer.CotMode]composerpb.CotMode{
	composer.CotModeDefault: composerpb.CotMode_COT_MODE_UNSPECIFIED,
	composer.CotModeFull:    composerpb.CotMode_COT_MODE_FULL,
	composer.CotModeMelody:  composerpb.CotMode_COT_MODE_MELODY,
	composer.CotModeOff:     composerpb.CotMode_COT_MODE_OFF,
}

var composerDecoderFromProto = map[composerpb.Decoder]composer.Decoder{
	composerpb.Decoder_DECODER_UNSPECIFIED: composer.DecoderDefault,
	composerpb.Decoder_DECODER_STANDARD:    composer.DecoderStandard,
	composerpb.Decoder_DECODER_LEGACY:      composer.DecoderLegacy,
}

var composerDecoderToProto = map[composer.Decoder]composerpb.Decoder{
	composer.DecoderDefault:  composerpb.Decoder_DECODER_UNSPECIFIED,
	composer.DecoderStandard: composerpb.Decoder_DECODER_STANDARD,
	composer.DecoderLegacy:   composerpb.Decoder_DECODER_LEGACY,
}

var composerStageToProto = map[composer.Stage]composerpb.ComposeProgress_Stage{
	composer.StageUnspecified:  composerpb.ComposeProgress_STAGE_UNSPECIFIED,
	composer.StageQueued:       composerpb.ComposeProgress_STAGE_QUEUED,
	composer.StageLoading:      composerpb.ComposeProgress_STAGE_LOADING,
	composer.StagePlanning:     composerpb.ComposeProgress_STAGE_PLANNING,
	composer.StageGenerating:   composerpb.ComposeProgress_STAGE_GENERATING,
	composer.StageSynthesizing: composerpb.ComposeProgress_STAGE_SYNTHESIZING,
	composer.StageDecoding:     composerpb.ComposeProgress_STAGE_DECODING,
	composer.StageTranscribing: composerpb.ComposeProgress_STAGE_TRANSCRIBING,
}

func composerSamplingFromProto(s *composerpb.SamplingOverrides) *composer.Sampling {
	if s == nil {
		return nil
	}

	return &composer.Sampling{
		Temperature:       s.Temperature,
		TopP:              s.TopP,
		TopK:              intPtr(s.TopK),
		RepetitionPenalty: s.RepetitionPenalty,
		PenaltyWindow:     intPtr(s.PenaltyWindow),
		MinTokens:         intPtr(s.MinTokens),
		MaxTokens:         intPtr(s.MaxTokens),
	}
}

func intPtr(p *int32) *int {
	if p == nil {
		return nil
	}

	v := int(*p)

	return &v
}

func composerProgressToProto(p composer.Progress) *composerpb.ComposeProgress {
	msg := &composerpb.ComposeProgress{
		Stage:         composerStageToProto[p.Stage],
		Completed:     p.Completed,
		Total:         p.Total,
		QueuePosition: int32(p.QueuePosition), //nolint:gosec // test-fixture positions fit int32
	}
	if p.Candidate != nil {
		candidate := int32(*p.Candidate) //nolint:gosec // test-fixture indexes fit int32
		msg.Candidate = &candidate
	}

	return msg
}

// effectiveSpec resolves the request defaults the way the real server does.
func effectiveSpec(spec *composerpb.SongSpec) (int64, composer.CotMode, string) {
	seed := int64(cannedComposerSeed)
	if spec.Seed != nil {
		seed = spec.GetSeed()
	}

	cot := composerCotFromProto[spec.GetCot()]
	if cot == composer.CotModeDefault {
		cot = composer.CotModeFull
	}

	abc := spec.GetAbc()
	if abc == "" && cot != composer.CotModeOff {
		abc = cannedComposerScore
	}

	return seed, cot, abc
}

func (h *composerHandler) Plan(
	_ context.Context,
	req *connect.Request[composerpb.PlanRequest],
	stream *connect.ServerStream[composerpb.PlanResponse],
) error {
	spec := req.Msg.GetSpec()
	if err := validateSpec(spec); err != nil {
		return err
	}

	seed, cot, abc := effectiveSpec(spec)
	plan := &composer.Plan{Seed: seed, Cot: cot, ABC: abc}

	if spec.GetInstrumental() {
		plan.ABCOriginal = abc
	}

	if h.fake.PlanFunc != nil {
		opts := composer.PlanOptions{
			Cot:          composerCotFromProto[spec.GetCot()],
			Seed:         spec.Seed,
			Instrumental: spec.GetInstrumental(),
			ABC:          spec.GetAbc(),
			ABCSampling:  composerSamplingFromProto(spec.GetAbcSampling()),
		}

		got, err := h.fake.PlanFunc(spec.GetStyle(), spec.GetLyrics(), opts)
		if err != nil {
			return err
		}

		plan = got
	}

	for _, p := range h.fake.Progress {
		err := stream.Send(&composerpb.PlanResponse{
			Event: &composerpb.PlanResponse_Progress{Progress: composerProgressToProto(p)},
		})
		if err != nil {
			return err
		}
	}

	result := &composerpb.PlanResult{
		Seed:      plan.Seed,
		Cot:       composerCotToProto[plan.Cot],
		Abc:       plan.ABC,
		Truncated: plan.Truncated,
	}
	if plan.ABCOriginal != "" {
		original := plan.ABCOriginal
		result.AbcOriginal = &original
	}

	return stream.Send(&composerpb.PlanResponse{
		Event: &composerpb.PlanResponse_Result{Result: result},
	})
}

func (h *composerHandler) ComposeSong(
	_ context.Context,
	req *connect.Request[composerpb.ComposeSongRequest],
	stream *connect.ServerStream[composerpb.ComposeSongResponse],
) error {
	spec := req.Msg.GetSpec()
	if err := validateSpec(spec); err != nil {
		return err
	}

	if req.Msg.GetCandidates() < 0 {
		return invalidArgument(errNegativeCandidates)
	}

	if int(req.Msg.GetCandidates()) > h.capabilities().MaxCandidates {
		return invalidArgument(errTooManyCandidates)
	}

	song := cannedSong(req.Msg)

	if h.fake.ComposeFunc != nil {
		opts := composer.SongOptions{
			Cot:              composerCotFromProto[spec.GetCot()],
			Seed:             spec.Seed,
			ABC:              spec.GetAbc(),
			CFGScale:         spec.CfgScale,
			Instrumental:     spec.GetInstrumental(),
			ABCSampling:      composerSamplingFromProto(spec.GetAbcSampling()),
			SemanticSampling: composerSamplingFromProto(spec.GetSemanticSampling()),
			Candidates:       int(req.Msg.GetCandidates()),
			Decoder:          composerDecoderFromProto[req.Msg.GetDecoder()],
		}

		got, err := h.fake.ComposeFunc(spec.GetStyle(), spec.GetLyrics(), opts)
		if err != nil {
			return err
		}

		song = got
	}

	for _, p := range h.fake.Progress {
		err := stream.Send(&composerpb.ComposeSongResponse{
			Event: &composerpb.ComposeSongResponse_Progress{Progress: composerProgressToProto(p)},
		})
		if err != nil {
			return err
		}
	}

	metadata := &composerpb.SongMetadata{
		Seed:             song.Seed,
		Cot:              composerCotToProto[song.Cot],
		Abc:              song.ABC,
		PlanTruncated:    song.PlanTruncated,
		Decoder:          composerDecoderToProto[song.Decoder],
		ProcessingTimeMs: song.ProcessingTime.Milliseconds(),
	}
	if song.ABCOriginal != "" {
		original := song.ABCOriginal
		metadata.AbcOriginal = &original
	}

	for _, c := range song.Candidates {
		metadata.Candidates = append(metadata.Candidates, &composerpb.CandidateMetadata{
			Index:           int32(c.Index), //nolint:gosec // test-fixture indexes fit int32
			Seed:            c.Seed,
			DurationSeconds: c.Duration.Seconds(),
			Truncated:       c.Truncated,
		})
	}

	err := stream.Send(&composerpb.ComposeSongResponse{
		Event: &composerpb.ComposeSongResponse_Metadata{Metadata: metadata},
	})
	if err != nil {
		return err
	}

	for i := range song.Candidates {
		err := SendFileChunks(&song.Candidates[i].Audio, func(chunk *commonpb.FileChunk) error {
			return stream.Send(&composerpb.ComposeSongResponse{
				Event: &composerpb.ComposeSongResponse_File{File: chunk},
			})
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// cannedSong is the nil-hook result: one take per requested candidate, seeds
// seed..seed+N-1, each a tiny FLAC payload.
func cannedSong(req *composerpb.ComposeSongRequest) *composer.Song {
	seed, cot, abc := effectiveSpec(req.GetSpec())

	if cot == composer.CotModeOff {
		abc = ""
	}

	song := &composer.Song{
		Seed:           seed,
		Cot:            cot,
		ABC:            abc,
		Decoder:        composerDecoderFromProto[req.GetDecoder()],
		ProcessingTime: 100 * time.Millisecond,
	}
	if song.Decoder == composer.DecoderDefault {
		song.Decoder = composer.DecoderStandard
	}

	if req.GetSpec().GetInstrumental() && abc != "" {
		song.ABCOriginal = abc
	}

	takes := max(int(req.GetCandidates()), 1)

	for k := range takes {
		song.Candidates = append(song.Candidates, composer.Candidate{
			Index:    k,
			Seed:     seed + int64(k),
			Duration: 3 * time.Second,
			Audio: rootpullersdk.File{
				Name:     fmt.Sprintf("song_%d.flac", k),
				MIMEType: "audio/flac",
				Data:     []byte{0x66, 0x4C, 0x61, 0x43},
			},
		})
	}

	return song
}

// Transcribe enforces config-first and reassembles the chunks.
func (h *composerHandler) Transcribe(
	_ context.Context,
	stream *connect.BidiStream[composerpb.TranscribeRequest, composerpb.TranscribeResponse],
) error {
	var (
		cfg   *composerpb.TranscribeConfig
		audio rootpullersdk.File
		seen  bool
	)

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		switch frame := req.GetFrame().(type) {
		case *composerpb.TranscribeRequest_Config:
			if cfg != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			cfg = frame.Config

		case *composerpb.TranscribeRequest_Chunk:
			if cfg == nil {
				return errBeforeParams("chunk")
			}

			if len(frame.Chunk.GetData()) > streamio.MaxChunkBytes {
				return invalidArgument(errChunkTooLarge)
			}

			if err := appendChunk(&audio, frame.Chunk); err != nil {
				return err
			}

			seen = true

		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if cfg == nil {
		return invalidArgument(errMissingParams)
	}

	if !seen {
		return invalidArgument(errMissingTranscriptionData)
	}

	melodyOnly := cfg.MelodyOnly == nil || cfg.GetMelodyOnly()
	result := &composer.Transcription{ABC: cannedComposerScore, MelodyOnly: melodyOnly}

	if h.fake.TranscribeFunc != nil {
		got, err := h.fake.TranscribeFunc(audio, melodyOnly)
		if err != nil {
			return err
		}

		result = got
	}

	for _, p := range h.fake.Progress {
		err := stream.Send(&composerpb.TranscribeResponse{
			Event: &composerpb.TranscribeResponse_Progress{Progress: composerProgressToProto(p)},
		})
		if err != nil {
			return err
		}
	}

	return stream.Send(&composerpb.TranscribeResponse{
		Event: &composerpb.TranscribeResponse_Result{
			Result: &composerpb.TranscriptionResult{Abc: result.ABC, MelodyOnly: result.MelodyOnly},
		},
	})
}

func (h *composerHandler) GetCapabilities(
	_ context.Context,
	_ *connect.Request[emptypb.Empty],
) (*connect.Response[composerpb.Capabilities], error) {
	c := h.capabilities()

	decoders := make([]composerpb.Decoder, 0, len(c.Decoders))
	for _, d := range c.Decoders {
		decoders = append(decoders, composerDecoderToProto[d])
	}

	return connect.NewResponse(&composerpb.Capabilities{
		Model:                    c.Model,
		ModelRevision:            c.ModelRevision,
		Decoders:                 decoders,
		Transcription:            c.Transcription,
		MaxCandidates:            int32(c.MaxCandidates), //nolint:gosec // test-fixture counts fit int32
		AbcSamplingDefaults:      composerSamplingDefaultsToProto(c.ABCSamplingDefaults),
		SemanticSamplingDefaults: composerSamplingDefaultsToProto(c.SemanticSamplingDefaults),
		SampleRate:               int32(c.SampleRate),     //nolint:gosec // test-fixture rates fit int32
		Channels:                 int32(c.Channels),       //nolint:gosec // test-fixture counts fit int32
		MaxStyleChars:            int32(c.MaxStyleChars),  //nolint:gosec // test-fixture limits fit int32
		MaxLyricsChars:           int32(c.MaxLyricsChars), //nolint:gosec // test-fixture limits fit int32
		MaxAbcChars:              int32(c.MaxABCChars),    //nolint:gosec // test-fixture limits fit int32
		MaxAudioBytes:            c.MaxAudioBytes,
	}), nil
}

func (h *composerHandler) capabilities() *composer.Capabilities {
	if h.fake.Capabilities != nil {
		return h.fake.Capabilities
	}

	return &composer.Capabilities{
		Model:         "m-a-p/YuE2-3B",
		ModelRevision: "fake",
		Decoders:      []composer.Decoder{composer.DecoderStandard, composer.DecoderLegacy},
		Transcription: true,
		MaxCandidates: 8,
		ABCSamplingDefaults: composer.SamplingDefaults{
			Temperature: 0.7, TopP: 0.9, TopK: 30, RepetitionPenalty: 1.005,
			PenaltyWindow: 100, MinTokens: 32, MaxTokens: 4096,
		},
		SemanticSamplingDefaults: composer.SamplingDefaults{
			Temperature: 1, TopP: 0.95, TopK: 100, RepetitionPenalty: 1.2,
			PenaltyWindow: 50, MinTokens: 200, MaxTokens: 9000,
		},
		SampleRate:     48000,
		Channels:       2,
		MaxStyleChars:  2000,
		MaxLyricsChars: 12000,
		MaxABCChars:    60000,
		MaxAudioBytes:  100 << 20,
	}
}

func composerSamplingDefaultsToProto(d composer.SamplingDefaults) *composerpb.SamplingDefaults {
	return &composerpb.SamplingDefaults{
		Temperature:       d.Temperature,
		TopP:              d.TopP,
		TopK:              int32(d.TopK), //nolint:gosec // test-fixture counts fit int32
		RepetitionPenalty: d.RepetitionPenalty,
		PenaltyWindow:     int32(d.PenaltyWindow), //nolint:gosec // test-fixture counts fit int32
		MinTokens:         int32(d.MinTokens),     //nolint:gosec // test-fixture counts fit int32
		MaxTokens:         int32(d.MaxTokens),     //nolint:gosec // test-fixture counts fit int32
	}
}
