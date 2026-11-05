// Package composer wraps com.entwico.rootpuller.composer.ComposerService:
// song generation from lyrics and a style prompt (YuE2), symbolic planning
// with editable ABC scores, recording-to-score transcription for covers, and
// capability discovery.
//
// The model first writes a melody-and-chord plan, then renders the song from
// it. Plan exposes the first half on its own: edit the returned score and
// hand it to ComposeSong via SongOptions.ABC. One generation runs at a time
// per deployment and takes minutes; every call streams progress through its
// options' OnProgress.
package composer

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	composerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/composer"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/composer/composerconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service is the composer client.
type Service struct {
	rpc        composerconnect.ComposerServiceClient
	deployment string
}

// Option configures a Service at construction.
type Option func(*Service)

// WithDeployment routes this service's calls at a named rootpuller
// deployment. A per-call rootpullersdk.ContextWithDeployment overrides it.
func WithDeployment(name string) Option {
	return func(s *Service) { s.deployment = name }
}

// NewService builds a Service on an existing SDK client.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	s := &Service{}
	for _, opt := range opts {
		opt(s)
	}

	core := sdk.TransportCore()
	s.rpc = composerconnect.NewComposerServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)

	return s
}

// Plan writes only the score: the melody-and-chord plan the song would
// follow, in a fraction of a song's time. Edit the returned ABC and hand it
// to ComposeSong via SongOptions.ABC, sending Plan.Seed along to keep
// everything else as it was.
//
// The call is the job: it returns when the plan is done, and cancelling ctx
// cancels the work on the server. A plan may first wait behind a running
// generation — watch opts.OnProgress.
func (s *Service) Plan(ctx context.Context, style, lyrics string, opts *PlanOptions) (*Plan, error) {
	if opts == nil {
		opts = &PlanOptions{}
	}

	spec, err := opts.song().spec(style, lyrics)
	if err != nil {
		return nil, err
	}

	ctx = transport.EnsureDeployment(ctx, s.deployment)
	procedure := composerconnect.ComposerServicePlanProcedure

	stream, serr := s.rpc.Plan(ctx, connect.NewRequest(&composerpb.PlanRequest{Spec: spec}))
	if serr != nil {
		return nil, transport.WrapError(serr, procedure)
	}

	defer func() { _ = stream.Close() }()

	var result *Plan

	for stream.Receive() {
		switch event := stream.Msg().GetEvent().(type) {
		case *composerpb.PlanResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(event.Progress))
			}

		case *composerpb.PlanResponse_Result:
			result = planFromProto(event.Result)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	if result == nil {
		return nil, apierr.New(connect.CodeInternal,
			"server ended the plan without a result", procedure, 0, nil)
	}

	return result, nil
}

// ComposeSong renders the song: plan (or follow opts.ABC), generate,
// synthesize, decode — minutes of GPU per take. The result holds every take
// as 48 kHz stereo FLAC; with opts.Candidates > 1 picking the best one is
// the caller's job.
//
// The call is the job: it returns when the song is done, and cancelling ctx
// cancels the generation on the server. Give ctx a deadline that allows for
// queueing plus minutes per take — or none — and use opts.OnProgress to see
// where it is.
func (s *Service) ComposeSong(ctx context.Context, style, lyrics string, opts *SongOptions) (*Song, error) {
	if opts == nil {
		opts = &SongOptions{}
	}

	msg, err := composeRequestToProto(style, lyrics, opts)
	if err != nil {
		return nil, err
	}

	ctx = transport.EnsureDeployment(ctx, s.deployment)
	procedure := composerconnect.ComposerServiceComposeSongProcedure

	stream, serr := s.rpc.ComposeSong(ctx, connect.NewRequest(msg))
	if serr != nil {
		return nil, transport.WrapError(serr, procedure)
	}

	defer func() { _ = stream.Close() }()

	var (
		song      *Song
		collector streamio.FileCollector
	)

	for stream.Receive() {
		switch event := stream.Msg().GetEvent().(type) {
		case *composerpb.ComposeSongResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(event.Progress))
			}

		case *composerpb.ComposeSongResponse_Metadata:
			song = songFromProto(event.Metadata)

		case *composerpb.ComposeSongResponse_File:
			collector.Add(event.File)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	if song == nil {
		return nil, apierr.New(connect.CodeInternal,
			"server ended the song without a metadata frame", procedure, 0, nil)
	}

	files, err := collector.Files()
	if err != nil {
		return nil, err
	}

	// The metadata names the takes; the files are their audio, streamed in
	// the same order. A mismatch is a truncated response, not a result.
	if len(files) != len(song.Candidates) {
		return nil, apierr.New(connect.CodeDataLoss,
			"server sent a different number of audio files than takes", procedure, 0, nil)
	}

	for i := range song.Candidates {
		song.Candidates[i].Audio = files[i]
	}

	return song, nil
}

// Transcribe turns a recording into an editable score (SheetSage2) — the
// first half of a cover. Hand Transcription.ABC to ComposeSong with cot
// melody (new accompaniment) or full (keep the harmony).
//
// Transcription is optional equipment on a deployment: without it the call
// fails with FAILED_PRECONDITION (check Capabilities.Transcription first).
func (s *Service) Transcribe(ctx context.Context, audio rootpullersdk.Upload, opts *TranscribeOptions) (*Transcription, error) {
	if opts == nil {
		opts = &TranscribeOptions{}
	}

	ctx = transport.EnsureDeployment(ctx, s.deployment)
	procedure := composerconnect.ComposerServiceTranscribeProcedure

	frames := streamio.Frames(
		&composerpb.TranscribeRequest{
			Frame: &composerpb.TranscribeRequest_Config{
				Config: &composerpb.TranscribeConfig{MelodyOnly: opts.MelodyOnly},
			},
		},
		streamio.FileChunkFrames(audio, func(chunk *commonpb.FileChunk) *composerpb.TranscribeRequest {
			return &composerpb.TranscribeRequest{
				Frame: &composerpb.TranscribeRequest_Chunk{Chunk: chunk},
			}
		}),
	)

	var result *Transcription

	err := streamio.UploadCollect(ctx, s.rpc.Transcribe, procedure, frames, func(resp *composerpb.TranscribeResponse) error {
		switch event := resp.GetEvent().(type) {
		case *composerpb.TranscribeResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(event.Progress))
			}

		case *composerpb.TranscribeResponse_Result:
			result = &Transcription{
				ABC:        event.Result.GetAbc(),
				MelodyOnly: event.Result.GetMelodyOnly(),
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if result == nil {
		return nil, apierr.New(connect.CodeInternal,
			"server ended the transcription without a result", procedure, 0, nil)
	}

	return result, nil
}

// GetCapabilities reports what the deployment serves and the limits to
// design around.
func (s *Service) GetCapabilities(ctx context.Context) (*Capabilities, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	resp, err := s.rpc.GetCapabilities(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, composerconnect.ComposerServiceGetCapabilitiesProcedure)
	}

	return capabilitiesFromProto(resp.Msg), nil
}

func composeRequestToProto(style, lyrics string, opts *SongOptions) (*composerpb.ComposeSongRequest, error) {
	spec, err := opts.spec(style, lyrics)
	if err != nil {
		return nil, err
	}

	decoder, err := opts.Decoder.toProto()
	if err != nil {
		return nil, err
	}

	if opts.Candidates < 0 {
		return nil, invalidArgument("composer: candidates must not be negative")
	}

	return &composerpb.ComposeSongRequest{
		Spec:       spec,
		Candidates: int32(opts.Candidates), //nolint:gosec // validated non-negative; counts fit int32
		Decoder:    decoder,
	}, nil
}

func songFromProto(m *composerpb.SongMetadata) *Song {
	candidates := make([]Candidate, 0, len(m.GetCandidates()))
	for _, c := range m.GetCandidates() {
		candidates = append(candidates, Candidate{
			Index:     int(c.GetIndex()),
			Seed:      c.GetSeed(),
			Duration:  time.Duration(c.GetDurationSeconds() * float64(time.Second)),
			Truncated: c.GetTruncated(),
		})
	}

	return &Song{
		Seed:           m.GetSeed(),
		Cot:            cotModeFromProto(m.GetCot()),
		ABC:            m.GetAbc(),
		ABCOriginal:    m.GetAbcOriginal(),
		PlanTruncated:  m.GetPlanTruncated(),
		Decoder:        decoderFromProto(m.GetDecoder()),
		Candidates:     candidates,
		ProcessingTime: time.Duration(m.GetProcessingTimeMs()) * time.Millisecond,
	}
}
