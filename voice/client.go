// Package voice wraps com.entwico.rootpuller.voice.VoiceService: synthetic
// voices designed from a text description, saved as permanent voices and
// speaking any text.
//
// The voice provider is the server's configuration choice; this package
// never names it. A saved voice is an opaque Ref, valid as long as the
// server keeps its provider. Keep each voice's sample audio (a preview's
// Audio, or what you cloned from): it is the voice's real identity. When the
// provider changes, Speak with an old Ref fails and IsProviderMismatch
// reports it; Save the sample again for a new Ref.
//
// The typical flow: Design previews from a description, listen, Save the
// one you like (passing its Handle and its Audio as the sample), store the
// Ref and the sample, then Speak. A talking-head video can speak a text in a
// saved voice directly; see video.Service.GenerateTalkingSpeech.
package voice

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	voicepb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/voice"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/voice/voiceconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// ReasonProviderMismatch is the google.rpc.ErrorInfo reason the server
// attaches when a Ref belongs to a provider it no longer uses.
const ReasonProviderMismatch = "VOICE_PROVIDER_MISMATCH"

// Service calls the VoiceService.
type Service struct {
	rpc voiceconnect.VoiceServiceClient
}

// NewService builds a VoiceService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client) *Service {
	core := sdk.TransportCore()

	return &Service{rpc: voiceconnect.NewVoiceServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// Design creates previews of new voices. The description says, in English,
// what the voice is: language, gender, age, timbre, accent, delivery. The
// sample text is what the previews say, in the voice's language; language is
// its ISO 639-1 code, e.g. "de" (ISO 639-3 for a language without one).
func (s *Service) Design(ctx context.Context, description, sampleText, language string, opts *DesignOptions) (*DesignResult, error) {
	if opts == nil {
		opts = &DesignOptions{}
	}

	msg, err := opts.toProto(description, sampleText, language)
	if err != nil {
		return nil, err
	}

	procedure := voiceconnect.VoiceServiceDesignVoiceProcedure

	stream, err := s.rpc.DesignVoice(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	defer func() { _ = stream.Close() }()

	var (
		meta  *voicepb.DesignMetadata
		files streamio.FileCollector
	)

	for stream.Receive() {
		switch event := stream.Msg().GetEvent().(type) {
		case *voicepb.DesignVoiceResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(event.Progress))
			}
		case *voicepb.DesignVoiceResponse_Metadata:
			meta = event.Metadata
		case *voicepb.DesignVoiceResponse_File:
			files.Add(event.File)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	if meta == nil {
		return nil, apierr.New(connect.CodeInternal, "server ended the design without a metadata frame", procedure, 0, nil)
	}

	audio, err := files.Files()
	if err != nil {
		return nil, err
	}

	// the metadata lists the previews; the files are their audio, streamed
	// in the same order.
	if len(audio) != len(meta.GetPreviews()) {
		return nil, apierr.New(connect.CodeDataLoss, "server sent a different number of audio files than previews", procedure, 0, nil)
	}

	out := &DesignResult{
		Previews:       make([]Preview, len(audio)),
		Model:          meta.GetModel(),
		SampleText:     meta.GetSampleText(),
		ProcessingTime: protoconv.MsToDuration(meta.GetProcessingTimeMs()),
		Usage:          usageFromProto(meta.GetUsage()),
	}

	for i, p := range meta.GetPreviews() {
		out.Previews[i] = Preview{
			Index:    int(p.GetIndex()),
			Handle:   p.GetHandle(),
			Audio:    audio[i],
			Duration: seconds(p.GetDurationSeconds()),
		}

		if p.ExpiresAtUnix != nil {
			t := unixTime(p.GetExpiresAtUnix())
			out.Previews[i].ExpiresAt = &t
		}
	}

	return out, nil
}

// Save turns a preview, or sample audio, into a permanent voice named name.
// The voice takes one of the provider's slots until Delete frees it.
func (s *Service) Save(ctx context.Context, name string, opts *SaveOptions) (*SaveResult, error) {
	if opts == nil {
		opts = &SaveOptions{}
	}

	if opts.Handle == "" && opts.Sample == nil {
		return nil, invalidArgument("voice: Save needs a Handle or a Sample")
	}

	procedure := voiceconnect.VoiceServiceSaveVoiceProcedure

	// a sample that fails to read cancels the call instead of half-closing
	// it: a clean close would look like a complete upload, and the server
	// would save a voice (and take a slot) whose ref nobody receives.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream := s.rpc.SaveVoice(ctx)

	params := &voicepb.SaveVoiceRequest_Params{Name: name, Description: opts.Description}
	if opts.Handle != "" {
		params.Handle = &opts.Handle
	}

	head := &voicepb.SaveVoiceRequest{Request: &voicepb.SaveVoiceRequest_Params_{Params: params}}
	if err := stream.Send(head); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, closeAndWrap(stream, err, procedure)
		}

		// nothing went out yet, so there is no request to abort, and closing
		// its response would wait for one that never comes.
		cancel()

		return nil, transport.WrapError(err, procedure)
	}

	if opts.Sample != nil {
		frames := streamio.FileChunkFrames(*opts.Sample, func(c *commonpb.FileChunk) *voicepb.SaveVoiceRequest {
			return &voicepb.SaveVoiceRequest{Request: &voicepb.SaveVoiceRequest_Sample{Sample: c}}
		})

		for frame, err := range frames {
			if err != nil {
				abort(stream, cancel)

				return nil, err
			}

			if serr := stream.Send(frame); serr != nil {
				return nil, sendFailed(stream, cancel, serr, procedure)
			}
		}
	}

	resp, err := stream.CloseAndReceive()
	if err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	return &SaveResult{
		Ref:        Ref(resp.Msg.GetVoiceRef()),
		Cloned:     resp.Msg.GetCloned(),
		SlotsUsed:  intPtr(resp.Msg.VoiceSlotsUsed),  //nolint:protogetter // presence matters
		SlotsLimit: intPtr(resp.Msg.VoiceSlotsLimit), //nolint:protogetter // presence matters
	}, nil
}

// sendFailed answers a failed sample Send. io.EOF means the server already
// ended the call, and its own error is the answer. Anything else failed on
// this side (a cancelled context, a chunk that would not marshal): the call
// is aborted, since a clean close would let the server save a partial sample.
func sendFailed(
	stream *connect.ClientStreamForClient[voicepb.SaveVoiceRequest, voicepb.SaveVoiceResponse],
	cancel context.CancelFunc,
	err error,
	procedure string,
) error {
	if errors.Is(err, io.EOF) {
		return closeAndWrap(stream, err, procedure)
	}

	abort(stream, cancel)

	return transport.WrapError(err, procedure)
}

// abort ends a SaveVoice call whose sample could not be sent. It never
// half-closes the request: cancellation reaches the transport asynchronously,
// so a CloseAndReceive after cancel can still deliver a clean end of stream,
// and the server would save a voice whose ref nobody receives. Closing only
// the response side leaves the request open until the cancellation resets
// the stream.
func abort(
	stream *connect.ClientStreamForClient[voicepb.SaveVoiceRequest, voicepb.SaveVoiceResponse],
	cancel context.CancelFunc,
) {
	cancel()

	if conn, err := stream.Conn(); err == nil {
		_ = conn.CloseResponse()
	}
}

func closeAndWrap(
	stream *connect.ClientStreamForClient[voicepb.SaveVoiceRequest, voicepb.SaveVoiceResponse],
	err error,
	procedure string,
) error {
	// after a Send failure the real reason comes from CloseAndReceive.
	if _, cerr := stream.CloseAndReceive(); cerr != nil {
		return transport.WrapError(cerr, procedure)
	}

	return transport.WrapError(err, procedure)
}

// Speak says text in a saved voice. language is the text's ISO 639-1 code (or 639-3),
// e.g. "de". A Ref from a provider the server no longer uses fails; see
// IsProviderMismatch.
func (s *Service) Speak(ctx context.Context, ref Ref, text, language string, opts *SpeakOptions) (*Speech, error) {
	if opts == nil {
		opts = &SpeakOptions{}
	}

	msg, err := opts.toProto(ref, text, language)
	if err != nil {
		return nil, err
	}

	procedure := voiceconnect.VoiceServiceSpeakProcedure

	stream, err := s.rpc.Speak(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	defer func() { _ = stream.Close() }()

	var (
		meta  *voicepb.SpeakMetadata
		files streamio.FileCollector
	)

	for stream.Receive() {
		switch event := stream.Msg().GetEvent().(type) {
		case *voicepb.SpeakResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(event.Progress))
			}
		case *voicepb.SpeakResponse_Metadata:
			meta = event.Metadata
		case *voicepb.SpeakResponse_File:
			files.Add(event.File)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	audio, err := files.Files()
	if err != nil {
		return nil, err
	}

	if meta == nil || len(audio) != 1 {
		return nil, apierr.New(connect.CodeInternal, "the server sent no complete speech", procedure, 0, nil)
	}

	return &Speech{
		Audio:          audio[0],
		Format:         formatFromProto(meta.GetFormat()),
		Model:          meta.GetModel(),
		Duration:       seconds(meta.GetDurationSeconds()),
		ProcessingTime: protoconv.MsToDuration(meta.GetProcessingTimeMs()),
		Usage:          usageFromProto(meta.GetUsage()),
	}, nil
}

// Delete deletes a saved voice and frees its slot. Deleting a voice the
// provider no longer has succeeds.
func (s *Service) Delete(ctx context.Context, ref Ref) error {
	_, err := s.rpc.DeleteVoice(ctx, connect.NewRequest(&voicepb.DeleteVoiceRequest{VoiceRef: string(ref)}))

	return transport.WrapError(err, voiceconnect.VoiceServiceDeleteVoiceProcedure)
}

// GetCapabilities reports what the server's voice provider supports, read
// live from it.
func (s *Service) GetCapabilities(ctx context.Context) (*Capabilities, error) {
	resp, err := s.rpc.GetCapabilities(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, voiceconnect.VoiceServiceGetCapabilitiesProcedure)
	}

	return capabilitiesFromProto(resp.Msg), nil
}

// IsProviderMismatch reports whether err is a Ref (or Handle) that belongs
// to a voice provider the server no longer uses. The voice is not lost:
// Save its sample again and store the new Ref.
func IsProviderMismatch(err error) bool {
	ce, ok := errors.AsType[*connect.Error](err)
	if !ok || ce.Code() != connect.CodeFailedPrecondition {
		return false
	}

	for _, d := range ce.Details() {
		v, verr := d.Value()
		if verr != nil {
			continue
		}

		if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetReason() == ReasonProviderMismatch {
			return true
		}
	}

	return false
}
