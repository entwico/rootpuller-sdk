// Package escriba wraps com.entwico.rootpuller.escriba.TranscriptionService:
// live microphone transcription over a bidirectional stream, one-shot
// transcription of a short clip, transcription of a complete recording of any
// length with optional speaker labels, and capability discovery.
//
// Live sessions distinguish provisional text from settled text, because the
// server cannot know a word is final until it has heard enough to stop revising
// it. See OpenLive and the Event type.
package escriba

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	escribapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba/escribaconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"

	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
)

// Service is the escriba client.
type Service struct {
	rpc             escribaconnect.TranscriptionServiceClient
	deployment      string
	defaultLanguage string
	defaultModel    string
}

// Option configures a Service at construction.
type Option func(*Service)

// WithDeployment routes this service's calls at a named escriba deployment.
// A per-call rootpullersdk.ContextWithEscribaDeployment overrides it. Leave
// unset to use whichever deployment the server treats as default.
func WithDeployment(name string) Option {
	return func(s *Service) { s.deployment = name }
}

// WithDefaultLanguage sets the language used when a call does not pin one.
func WithDefaultLanguage(code string) Option {
	return func(s *Service) { s.defaultLanguage = code }
}

// WithDefaultModel sets the model used when a call does not pin one.
func WithDefaultModel(modelID string) Option {
	return func(s *Service) { s.defaultModel = modelID }
}

// NewService builds a Service on an existing SDK client.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	s := &Service{}
	for _, opt := range opts {
		opt(s)
	}

	core := sdk.TransportCore()
	s.rpc = escribaconnect.NewTranscriptionServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)

	return s
}

// Transcribe transcribes one complete recording.
//
// Intended for short recordings — voice notes, captured utterances, language
// probes. The server holds an inference slot for the whole decode, so anything
// longer than Capabilities.MaxRecording belongs to TranscribeRecording.
func (s *Service) Transcribe(
	ctx context.Context,
	audio rootpullersdk.Upload,
	opts *TranscribeOptions,
) (*Transcript, error) {
	if opts == nil {
		opts = &TranscribeOptions{}
	}

	ctx = transport.EnsureEscribaDeployment(ctx, s.deployment)
	procedure := escribaconnect.TranscriptionServiceTranscribeProcedure
	stream := s.rpc.Transcribe(ctx)

	cfg := &escribapb.TranscribeConfig{
		Language:     s.languageOr(opts.Language),
		IncludeWords: opts.IncludeWords,
	}
	if model := s.modelOr(opts.Model); model != "" {
		cfg.Model = &escribapb.TranscriptionModelRef{ModelId: model}
	}

	if err := stream.Send(&escribapb.TranscribeRequest{
		Frame: &escribapb.TranscribeRequest_Config{Config: cfg},
	}); err != nil {
		return nil, closeAndWrap(stream, err, procedure)
	}

	frames := streamio.FileChunkFrames(audio, func(chunk *commonpb.FileChunk) *escribapb.TranscribeRequest {
		return &escribapb.TranscribeRequest{
			Frame: &escribapb.TranscribeRequest_Chunk{Chunk: chunk},
		}
	})

	for frame, err := range frames {
		if err != nil {
			_, _ = stream.CloseAndReceive()

			return nil, err
		}

		if serr := stream.Send(frame); serr != nil {
			return nil, closeAndWrap(stream, serr, procedure)
		}
	}

	resp, err := stream.CloseAndReceive()
	if err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	return transcriptFromProto(resp.Msg), nil
}

// GetCapabilities reports the served models and the limits to design around.
func (s *Service) GetCapabilities(ctx context.Context) (*Capabilities, error) {
	ctx = transport.EnsureEscribaDeployment(ctx, s.deployment)

	resp, err := s.rpc.GetCapabilities(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, escribaconnect.TranscriptionServiceGetCapabilitiesProcedure)
	}

	return capabilitiesFromProto(resp.Msg), nil
}

func (s *Service) languageOr(requested string) string {
	if requested != "" {
		return requested
	}

	return s.defaultLanguage
}

func (s *Service) modelOr(requested string) string {
	if requested != "" {
		return requested
	}

	return s.defaultModel
}

func invalidArgument(message string) error {
	return apierr.New(connect.CodeInvalidArgument, message, "", 0, nil)
}

// closeAndWrap surfaces the definitive error after a failed Send.
func closeAndWrap(
	stream *connect.ClientStreamForClient[escribapb.TranscribeRequest, escribapb.TranscribeResponse],
	err error,
	procedure string,
) error {
	// After a Send failure the real reason comes from CloseAndReceive.
	if _, cerr := stream.CloseAndReceive(); cerr != nil {
		return transport.WrapError(cerr, procedure)
	}

	return transport.WrapError(err, procedure)
}
