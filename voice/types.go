package voice

import (
	"fmt"
	"math"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	voicepb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/voice"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// MaxPreviews is the most previews one Design call returns.
const MaxPreviews = 4

// Ref identifies a saved voice. It is opaque: store it as-is and pass it to
// Speak and Delete. It stays valid as long as the server's voice provider
// does not change; see IsProviderMismatch.
type Ref string

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// DesignOptions shapes a Design call. Every field is optional.
type DesignOptions struct {
	// Previews is the number of previews, 1-MaxPreviews, default 3. A
	// provider that always returns a fixed number may return that many.
	Previews *int

	// Model overrides the server's default design model; see
	// Capabilities.DesignModels.
	Model string

	// OnProgress, when set, receives progress frames as the server reports
	// them. Called synchronously from the receive loop.
	OnProgress func(Progress)
}

func (o *DesignOptions) toProto(description, sampleText, language string) (*voicepb.DesignVoiceRequest, error) {
	if o.Previews != nil && (*o.Previews < 1 || *o.Previews > MaxPreviews) {
		return nil, invalidArgument(fmt.Sprintf("voice: Previews must be between 1 and %d", MaxPreviews))
	}

	msg := &voicepb.DesignVoiceRequest{
		Description: description,
		SampleText:  sampleText,
		Language:    language,
		Previews:    protoconv.Int32Ptr(o.Previews),
	}

	if o.Model != "" {
		msg.Model = &o.Model
	}

	return msg, nil
}

// Preview is one designed voice.
type Preview struct {
	// Index matches the audio file's name, "preview_<index>.mp3".
	Index int

	// Handle saves this preview with Save. It is valid for a limited time;
	// keep Audio too, which Save also accepts as a sample.
	Handle string

	// Audio is the preview speaking the sample text.
	Audio    rootpullersdk.File
	Duration time.Duration

	// ExpiresAt is when Handle stops working; nil when the provider does
	// not say.
	ExpiresAt *time.Time
}

// DesignResult is the outcome of Design.
type DesignResult struct {
	Previews []Preview

	// Model is the design model actually used.
	Model string

	// SampleText is what the previews say.
	SampleText string

	ProcessingTime time.Duration
	Usage          Usage
}

// SaveOptions says what to save. Set Handle, Sample, or both: with both, the
// preview is saved while Handle is valid and Sample is cloned once it has
// expired.
type SaveOptions struct {
	// Description is stored with the voice.
	Description string

	// Handle is a Preview.Handle from Design.
	Handle string

	// Sample is audio to clone the voice from (MP3, WAV, FLAC, M4A or OGG),
	// up to 64 MiB, typically a preview's Audio.AsUpload(). Keep it: it is
	// the voice's identity and re-creates the voice when the server's
	// provider changes.
	Sample *rootpullersdk.Upload
}

// SaveResult is a saved voice.
type SaveResult struct {
	Ref Ref

	// Cloned reports that the voice was cloned from the sample rather than
	// saved from the preview.
	Cloned bool

	// SlotsUsed and SlotsLimit are the provider's saved-voice slots after
	// saving, when it reports them.
	SlotsUsed  *int
	SlotsLimit *int
}

// SpeakOptions shapes speech. Every field is optional.
type SpeakOptions struct {
	// Model overrides the server's default speech model; see
	// Capabilities.SpeechModels.
	Model string

	// Speed is the speaking rate, 0-1: 0.5 is the voice's natural pace, 0
	// the slowest. Nil keeps the natural pace.
	Speed *float64

	// Stability is 0-1: low is more expressive and varied, high more even.
	// Default: the provider's.
	Stability *float64

	// OnProgress, when set, receives progress frames as the server reports
	// them. Called synchronously from the receive loop.
	OnProgress func(Progress)
}

func (o *SpeakOptions) toProto(ref Ref, text, language string) (*voicepb.SpeakRequest, error) {
	msg := &voicepb.SpeakRequest{VoiceRef: string(ref), Text: text, Language: language}

	if o.Model != "" {
		msg.Model = &o.Model
	}

	if err := unitRange("Speed", o.Speed); err != nil {
		return nil, err
	}

	if err := unitRange("Stability", o.Stability); err != nil {
		return nil, err
	}

	msg.Speed = float32Ptr(o.Speed)
	msg.Stability = float32Ptr(o.Stability)

	return msg, nil
}

// unitRange checks an optional 0-1 value; written so NaN fails too.
func unitRange(field string, v *float64) error {
	if v != nil && !(*v >= 0 && *v <= 1) {
		return invalidArgument("voice: " + field + " must be between 0 and 1")
	}

	return nil
}

func float32Ptr(v *float64) *float32 {
	if v == nil {
		return nil
	}

	f := float32(*v)

	return &f
}

// Speech is a spoken text.
type Speech struct {
	// Audio is "speech_0.mp3" (audio/mpeg).
	Audio rootpullersdk.File

	// Model is the speech model actually used.
	Model          string
	Duration       time.Duration
	ProcessingTime time.Duration
	Usage          Usage
}

// Usage is what one call cost at the voice provider.
type Usage struct {
	// Characters is what the provider billed.
	Characters int64

	// EstimatedCostMicros is the estimated cost in millionths of
	// CurrencyCode; 0 when unknown.
	EstimatedCostMicros int64
	CurrencyCode        string
}

func usageFromProto(u *voicepb.Usage) Usage {
	return Usage{
		Characters:          u.GetCharacters(),
		EstimatedCostMicros: u.GetEstimatedCostMicros(),
		CurrencyCode:        u.GetCurrencyCode(),
	}
}

// Capabilities is what the server's voice provider supports.
type Capabilities struct {
	CanDesign bool
	CanClone  bool

	// SlotsUsed and SlotsLimit are the saved-voice slots, when reported.
	SlotsUsed  *int
	SlotsLimit *int

	// SpeechModels and DesignModels list the models, the default first.
	SpeechModels []Model
	DesignModels []Model

	// MaxTextChars is the longest text Speak accepts with the default
	// speech model.
	MaxTextChars int

	// CharactersUsed and CharactersLimit are the provider's character quota
	// for the current period, when reported.
	CharactersUsed  *int64
	CharactersLimit *int64

	// DescriptionLimits and SampleTextLimits bound Design's description and
	// sample text.
	DescriptionLimits TextLimits
	SampleTextLimits  TextLimits

	// MaxSampleSeconds is the longest part of a Save sample the provider
	// uses; 0 when it does not say.
	MaxSampleSeconds int
}

// TextLimits is a length range in characters; 0 means no bound on that
// side.
type TextLimits struct {
	MinChars int
	MaxChars int
}

// Model is one provider model.
type Model struct {
	ID   string
	Name string

	// Languages are the codes it speaks (ISO 639-1, or 639-3 where none
	// exists); empty when unknown.
	Languages []string

	// MaxTextChars is the longest text one Speak call accepts with this
	// model; 0 when unknown.
	MaxTextChars int
}

func capabilitiesFromProto(c *voicepb.Capabilities) *Capabilities {
	return &Capabilities{
		CanDesign:       c.GetCanDesign(),
		CanClone:        c.GetCanClone(),
		SlotsUsed:       intPtr(c.VoiceSlotsUsed),
		SlotsLimit:      intPtr(c.VoiceSlotsLimit),
		SpeechModels:    modelsFromProto(c.GetSpeechModels()),
		DesignModels:    modelsFromProto(c.GetDesignModels()),
		MaxTextChars:    int(c.GetMaxTextChars()),
		CharactersUsed:  c.CharactersUsed,
		CharactersLimit: c.CharactersLimit,
		DescriptionLimits: TextLimits{
			MinChars: int(c.GetDescriptionLimits().GetMinChars()),
			MaxChars: int(c.GetDescriptionLimits().GetMaxChars()),
		},
		SampleTextLimits: TextLimits{
			MinChars: int(c.GetSampleTextLimits().GetMinChars()),
			MaxChars: int(c.GetSampleTextLimits().GetMaxChars()),
		},
		MaxSampleSeconds: int(c.GetMaxSampleSeconds()),
	}
}

func modelsFromProto(models []*voicepb.ModelInfo) []Model {
	out := make([]Model, len(models))
	for i, m := range models {
		out[i] = Model{ID: m.GetId(), Name: m.GetName(), Languages: m.GetLanguages(), MaxTextChars: int(m.GetMaxTextChars())}
	}

	return out
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}

	i := int(*v)

	return &i
}

// Stage is the coarse state of a call.
type Stage string

const (
	StageUnspecified Stage = ""
	// StageQueued: waiting for the provider.
	StageQueued Stage = "queued"
	// StageGenerating: the provider is generating; Message may say which
	// preview.
	StageGenerating Stage = "generating"
	// StageUploading: the audio is being sent back.
	StageUploading Stage = "uploading"
)

var stageFromProto = map[voicepb.Progress_Stage]Stage{
	voicepb.Progress_STAGE_UNSPECIFIED: StageUnspecified,
	voicepb.Progress_STAGE_QUEUED:      StageQueued,
	voicepb.Progress_STAGE_GENERATING:  StageGenerating,
	voicepb.Progress_STAGE_UPLOADING:   StageUploading,
}

// Progress is one progress frame.
type Progress struct {
	Stage Stage
	// Percentage is the overall progress 0.0-1.0, never decreasing.
	Percentage float32
	Message    string
}

func progressFromProto(p *voicepb.Progress) Progress {
	stage, ok := stageFromProto[p.GetStage()]
	if !ok {
		// a stage newer than this SDK keeps its proto name.
		stage = Stage(p.GetStage().String())
	}

	return Progress{Stage: stage, Percentage: p.GetPercentage(), Message: p.GetMessage()}
}

func seconds(s float32) time.Duration {
	return time.Duration(math.Round(float64(s) * float64(time.Second)))
}

func unixTime(sec int64) time.Time {
	return time.Unix(sec, 0)
}

func millis(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
