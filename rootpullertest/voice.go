package rootpullertest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	voicepb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/voice"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/voice/voiceconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/voice"
)

// Voice is a facade-typed fake VoiceService. It enforces the wire protocol
// like the real server: Design and Speak need their texts and a language;
// SaveVoice takes params first and exactly once, then sample chunks (capped
// at 2 MiB each), and needs a name and a handle or a sample; DeleteVoice
// needs a ref. Text lengths are checked against the limits it reports from
// GetCapabilities (Capabilities, or defaults matching ElevenLabs: design
// description 20-1000 and sample text 100-1000 characters, speech text up to
// 5000, or the chosen model's MaxTextChars). Violations fail with
// InvalidArgument.
//
// Nil hooks answer with canned data: three previews with handles
// "handle-0".."handle-2", a ref "fake-ref:<name>" (cloned when no handle was
// sent), a canned "speech_0.mp3", and Delete succeeds. Progress frames are
// streamed before the metadata on Design and Speak. Return
// VoiceProviderMismatch() from a hook to simulate a provider switch.
type Voice struct {
	DesignFunc   func(req VoiceDesignRequest) (*voice.DesignResult, error)
	SaveFunc     func(req VoiceSaveRequest) (*voice.SaveResult, error)
	SpeakFunc    func(req VoiceSpeakRequest) (*voice.Speech, error)
	DeleteFunc   func(ref voice.Ref) error
	Capabilities *voice.Capabilities
	Progress     []voice.Progress
}

// VoiceDesignRequest is what the fake received on one DesignVoice call.
type VoiceDesignRequest struct {
	Description string
	SampleText  string
	Language    string
	Previews    *int
	Model       *string
}

// VoiceSaveRequest is what the fake received on one SaveVoice call.
type VoiceSaveRequest struct {
	Name        string
	Description string
	Handle      *string
	Sample      *rootpullersdk.File
}

// VoiceSpeakRequest is what the fake received on one Speak call.
type VoiceSpeakRequest struct {
	Ref       voice.Ref
	Text      string
	Language  string
	Model     *string
	Speed     *float64
	Stability *float64
}

// VoiceProviderMismatch is the error the server returns for a voice.Ref
// from a provider it no longer uses; voice.IsProviderMismatch recognises it.
func VoiceProviderMismatch() error {
	err := connect.NewError(connect.CodeFailedPrecondition, errVoiceProviderMismatch)

	detail, derr := connect.NewErrorDetail(&errdetails.ErrorInfo{
		Reason:   voice.ReasonProviderMismatch,
		Domain:   "rootpuller",
		Metadata: map[string]string{"ref_provider": "old", "active_provider": "fake"},
	})
	if derr == nil {
		err.AddDetail(detail)
	}

	return err
}

var (
	errVoiceProviderMismatch = errors.New("the voice was saved with another provider")
	errVoiceMissingText      = errors.New("description, sample_text and language are required")
	errVoiceMissingName      = errors.New("name is required")
	errVoiceMissingRef       = errors.New("voice_ref is required")
	errVoiceTextLength       = errors.New("text length outside the provider's limits")
	errVoiceMissingSpeak     = errors.New("voice_ref, text and language are required")
	errVoiceNothingToSave    = errors.New("a handle or a sample is required")
)

const fakeVoiceMime = "audio/mpeg"

func (f *Voice) register(mux *http.ServeMux) {
	mux.Handle(voiceconnect.NewVoiceServiceHandler(&voiceHandler{fake: f}))
}

type voiceHandler struct {
	voiceconnect.UnimplementedVoiceServiceHandler

	fake *Voice
}

func (h *voiceHandler) DesignVoice(
	_ context.Context,
	req *connect.Request[voicepb.DesignVoiceRequest],
	stream *connect.ServerStream[voicepb.DesignVoiceResponse],
) error {
	m := req.Msg
	if m.GetDescription() == "" || m.GetSampleText() == "" || m.GetLanguage() == "" {
		return invalidArgument(errVoiceMissingText)
	}

	caps := h.capabilities()
	if !withinLimits(m.GetDescription(), caps.DescriptionLimits) || !withinLimits(m.GetSampleText(), caps.SampleTextLimits) {
		return invalidArgument(errVoiceTextLength)
	}

	got := VoiceDesignRequest{
		Description: m.GetDescription(),
		SampleText:  m.GetSampleText(),
		Language:    m.GetLanguage(),
		Previews:    intPtr(m.Previews), //nolint:protogetter // presence matters
		Model:       m.Model,
	}

	result := cannedDesign(got)

	if h.fake.DesignFunc != nil {
		var err error
		if result, err = h.fake.DesignFunc(got); err != nil {
			return err
		}
	}

	if err := h.sendProgress(func(p *voicepb.Progress) error {
		return stream.Send(&voicepb.DesignVoiceResponse{Event: &voicepb.DesignVoiceResponse_Progress{Progress: p}})
	}); err != nil {
		return err
	}

	meta := &voicepb.DesignMetadata{
		Model:            result.Model,
		SampleText:       result.SampleText,
		ProcessingTimeMs: result.ProcessingTime.Milliseconds(),
		Usage:            voiceUsageToProto(result.Usage),
	}

	for i, p := range result.Previews {
		pm := &voicepb.PreviewMetadata{
			Index:           int32(i),
			Handle:          p.Handle,
			DurationSeconds: float32(p.Duration.Seconds()),
		}

		if p.ExpiresAt != nil {
			unix := p.ExpiresAt.Unix()
			pm.ExpiresAtUnix = &unix
		}

		meta.Previews = append(meta.Previews, pm)
	}

	if err := stream.Send(&voicepb.DesignVoiceResponse{Event: &voicepb.DesignVoiceResponse_Metadata{Metadata: meta}}); err != nil {
		return err
	}

	for i, p := range result.Previews {
		file := p.Audio
		file.Name = fmt.Sprintf("preview_%d.mp3", i)

		err := SendFileChunks(&file, func(c *commonpb.FileChunk) error {
			return stream.Send(&voicepb.DesignVoiceResponse{Event: &voicepb.DesignVoiceResponse_File{File: c}})
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func cannedDesign(req VoiceDesignRequest) *voice.DesignResult {
	n := 3
	if req.Previews != nil {
		n = *req.Previews
	}

	out := &voice.DesignResult{Model: "fake-design", SampleText: req.SampleText}

	for i := range n {
		out.Previews = append(out.Previews, voice.Preview{
			Index:  i,
			Handle: fmt.Sprintf("handle-%d", i),
			Audio:  rootpullersdk.File{MIMEType: fakeVoiceMime, Data: []byte{0xff, 0xfb, byte(i)}},
		})
	}

	return out
}

func (h *voiceHandler) SaveVoice(
	_ context.Context,
	stream *connect.ClientStream[voicepb.SaveVoiceRequest],
) (*connect.Response[voicepb.SaveVoiceResponse], error) {
	var (
		params *voicepb.SaveVoiceRequest_Params
		sample *rootpullersdk.File
	)

	for stream.Receive() {
		switch r := stream.Msg().GetRequest().(type) {
		case *voicepb.SaveVoiceRequest_Params_:
			if params != nil {
				return nil, invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *voicepb.SaveVoiceRequest_Sample:
			if params == nil {
				return nil, errBeforeParams("sample")
			}

			if err := appendFrame(&sample, r.Sample); err != nil {
				return nil, err
			}
		default:
			return nil, invalidArgument(errUnexpectedVariant)
		}
	}

	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	switch {
	case params == nil:
		return nil, invalidArgument(errMissingParams)
	case params.GetName() == "":
		return nil, invalidArgument(errVoiceMissingName)
	case params.Handle == nil && sample == nil:
		return nil, invalidArgument(errVoiceNothingToSave)
	}

	got := VoiceSaveRequest{
		Name:        params.GetName(),
		Description: params.GetDescription(),
		Handle:      params.Handle,
		Sample:      sample,
	}

	result := &voice.SaveResult{Ref: voice.Ref("fake-ref:" + got.Name), Cloned: got.Handle == nil}

	if h.fake.SaveFunc != nil {
		var err error
		if result, err = h.fake.SaveFunc(got); err != nil {
			return nil, err
		}
	}

	return connect.NewResponse(&voicepb.SaveVoiceResponse{
		VoiceRef:        string(result.Ref),
		Cloned:          result.Cloned,
		VoiceSlotsUsed:  protoconv.Int32Ptr(result.SlotsUsed),
		VoiceSlotsLimit: protoconv.Int32Ptr(result.SlotsLimit),
	}), nil
}

func (h *voiceHandler) Speak(
	_ context.Context,
	req *connect.Request[voicepb.SpeakRequest],
	stream *connect.ServerStream[voicepb.SpeakResponse],
) error {
	m := req.Msg
	if m.GetVoiceRef() == "" || m.GetText() == "" || m.GetLanguage() == "" {
		return invalidArgument(errVoiceMissingSpeak)
	}

	if limit := h.textLimit(m.GetModel()); limit > 0 && utf8.RuneCountInString(m.GetText()) > limit {
		return invalidArgument(errVoiceTextLength)
	}

	got := VoiceSpeakRequest{
		Ref:       voice.Ref(m.GetVoiceRef()),
		Text:      m.GetText(),
		Language:  m.GetLanguage(),
		Model:     m.Model,
		Speed:     float64Ptr(m.Speed),
		Stability: float64Ptr(m.Stability),
	}

	speech := &voice.Speech{
		Audio: rootpullersdk.File{MIMEType: fakeVoiceMime, Data: []byte{0xff, 0xfb, 0x90, 0x00}},
		Model: "fake-speech",
		Usage: voice.Usage{Characters: int64(len([]rune(got.Text)))},
	}

	if h.fake.SpeakFunc != nil {
		var err error
		if speech, err = h.fake.SpeakFunc(got); err != nil {
			return err
		}
	}

	if err := h.sendProgress(func(p *voicepb.Progress) error {
		return stream.Send(&voicepb.SpeakResponse{Event: &voicepb.SpeakResponse_Progress{Progress: p}})
	}); err != nil {
		return err
	}

	meta := &voicepb.SpeakMetadata{
		Model:            speech.Model,
		DurationSeconds:  float32(speech.Duration.Seconds()),
		ProcessingTimeMs: speech.ProcessingTime.Milliseconds(),
		Usage:            voiceUsageToProto(speech.Usage),
	}

	if err := stream.Send(&voicepb.SpeakResponse{Event: &voicepb.SpeakResponse_Metadata{Metadata: meta}}); err != nil {
		return err
	}

	file := speech.Audio
	file.Name = "speech_0.mp3"

	return SendFileChunks(&file, func(c *commonpb.FileChunk) error {
		return stream.Send(&voicepb.SpeakResponse{Event: &voicepb.SpeakResponse_File{File: c}})
	})
}

func (h *voiceHandler) DeleteVoice(
	_ context.Context,
	req *connect.Request[voicepb.DeleteVoiceRequest],
) (*connect.Response[voicepb.DeleteVoiceResponse], error) {
	if req.Msg.GetVoiceRef() == "" {
		return nil, invalidArgument(errVoiceMissingRef)
	}

	if h.fake.DeleteFunc != nil {
		if err := h.fake.DeleteFunc(voice.Ref(req.Msg.GetVoiceRef())); err != nil {
			return nil, err
		}
	}

	return connect.NewResponse(&voicepb.DeleteVoiceResponse{}), nil
}

func (h *voiceHandler) GetCapabilities(
	context.Context,
	*connect.Request[emptypb.Empty],
) (*connect.Response[voicepb.Capabilities], error) {
	c := h.capabilities()

	return connect.NewResponse(&voicepb.Capabilities{
		CanDesign:       c.CanDesign,
		CanClone:        c.CanClone,
		VoiceSlotsUsed:  protoconv.Int32Ptr(c.SlotsUsed),
		VoiceSlotsLimit: protoconv.Int32Ptr(c.SlotsLimit),
		SpeechModels:    voiceModelsToProto(c.SpeechModels),
		DesignModels:    voiceModelsToProto(c.DesignModels),
		MaxTextChars:    int32(c.MaxTextChars), //nolint:gosec // test fixture limits
		CharactersUsed:  c.CharactersUsed,
		CharactersLimit: c.CharactersLimit,
		DescriptionLimits: &voicepb.TextLimits{
			MinChars: int32(c.DescriptionLimits.MinChars), //nolint:gosec // test fixture limits
			MaxChars: int32(c.DescriptionLimits.MaxChars), //nolint:gosec // test fixture limits
		},
		SampleTextLimits: &voicepb.TextLimits{
			MinChars: int32(c.SampleTextLimits.MinChars), //nolint:gosec // test fixture limits
			MaxChars: int32(c.SampleTextLimits.MaxChars), //nolint:gosec // test fixture limits
		},
		MaxSampleSeconds: int32(c.MaxSampleSeconds), //nolint:gosec // test fixture limits
	}), nil
}

// capabilities is what the fake reports and enforces: Capabilities, or
// defaults matching the ElevenLabs provider.
func (h *voiceHandler) capabilities() voice.Capabilities {
	if h.fake.Capabilities != nil {
		return *h.fake.Capabilities
	}

	return voice.Capabilities{
		CanDesign:         true,
		CanClone:          true,
		SpeechModels:      []voice.Model{{ID: "fake-speech", Name: "Fake speech", Languages: []string{"de", "en"}, MaxTextChars: 5000}},
		DesignModels:      []voice.Model{{ID: "fake-design", Name: "Fake design"}},
		MaxTextChars:      5000,
		DescriptionLimits: voice.TextLimits{MinChars: 20, MaxChars: 1000},
		SampleTextLimits:  voice.TextLimits{MinChars: 100, MaxChars: 1000},
		MaxSampleSeconds:  120,
	}
}

// textLimit is the longest Speak text for model ("" = the default).
func (h *voiceHandler) textLimit(model string) int {
	c := h.capabilities()

	for _, m := range c.SpeechModels {
		if m.ID == model && m.MaxTextChars > 0 {
			return m.MaxTextChars
		}
	}

	return c.MaxTextChars
}

func withinLimits(s string, l voice.TextLimits) bool {
	n := utf8.RuneCountInString(s)

	return (l.MinChars == 0 || n >= l.MinChars) && (l.MaxChars == 0 || n <= l.MaxChars)
}

func (h *voiceHandler) sendProgress(send func(*voicepb.Progress) error) error {
	for _, p := range h.fake.Progress {
		if err := send(&voicepb.Progress{Stage: voiceStageToProto[p.Stage], Percentage: p.Percentage, Message: p.Message}); err != nil {
			return err
		}
	}

	return nil
}

var voiceStageToProto = map[voice.Stage]voicepb.Progress_Stage{
	voice.StageUnspecified: voicepb.Progress_STAGE_UNSPECIFIED,
	voice.StageQueued:      voicepb.Progress_STAGE_QUEUED,
	voice.StageGenerating:  voicepb.Progress_STAGE_GENERATING,
	voice.StageUploading:   voicepb.Progress_STAGE_UPLOADING,
}

func voiceUsageToProto(u voice.Usage) *voicepb.Usage {
	return &voicepb.Usage{
		Characters:          u.Characters,
		EstimatedCostMicros: u.EstimatedCostMicros,
		CurrencyCode:        u.CurrencyCode,
	}
}

func voiceModelsToProto(models []voice.Model) []*voicepb.ModelInfo {
	out := make([]*voicepb.ModelInfo, len(models))
	for i, m := range models {
		out[i] = &voicepb.ModelInfo{
			Id: m.ID, Name: m.Name, Languages: m.Languages,
			MaxTextChars: int32(m.MaxTextChars), //nolint:gosec // test fixture limits
		}
	}

	return out
}

func float64Ptr(v *float32) *float64 {
	if v == nil {
		return nil
	}

	f := float64(*v)

	return &f
}
