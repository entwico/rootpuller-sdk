package composer

import (
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	composerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/composer"
)

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// CotMode is how much of the song is planned symbolically before rendering.
// The zero value keeps the server default (full).
type CotMode string

const (
	CotModeDefault CotMode = ""
	// CotModeFull plans melody and chords.
	CotModeFull CotMode = "full"
	// CotModeMelody plans only the melody; the accompaniment is left free.
	CotModeMelody CotMode = "melody"
	// CotModeOff renders straight from lyrics and style, with no score.
	CotModeOff CotMode = "off"
)

func (m CotMode) toProto() (composerpb.CotMode, error) {
	switch m {
	case CotModeDefault:
		return composerpb.CotMode_COT_MODE_UNSPECIFIED, nil
	case CotModeFull:
		return composerpb.CotMode_COT_MODE_FULL, nil
	case CotModeMelody:
		return composerpb.CotMode_COT_MODE_MELODY, nil
	case CotModeOff:
		return composerpb.CotMode_COT_MODE_OFF, nil
	default:
		return 0, invalidArgument("composer: unknown cot mode " + string(m))
	}
}

func cotModeFromProto(m composerpb.CotMode) CotMode {
	switch m {
	case composerpb.CotMode_COT_MODE_UNSPECIFIED:
		return CotModeDefault
	case composerpb.CotMode_COT_MODE_FULL:
		return CotModeFull
	case composerpb.CotMode_COT_MODE_MELODY:
		return CotModeMelody
	case composerpb.CotMode_COT_MODE_OFF:
		return CotModeOff
	default:
		return CotMode(m.String())
	}
}

// Decoder picks the VAE that turns acoustic latents into audio. The zero
// value keeps the server default (standard).
type Decoder string

const (
	DecoderDefault Decoder = ""
	// DecoderStandard is YuE2-Vae, the listening decoder.
	DecoderStandard Decoder = "standard"
	// DecoderLegacy is YuE2-Vae-legacy, the decoder behind the published
	// benchmarks. Requires its weights on the deployment
	// (Capabilities.Decoders).
	DecoderLegacy Decoder = "legacy"
)

func (d Decoder) toProto() (composerpb.Decoder, error) {
	switch d {
	case DecoderDefault:
		return composerpb.Decoder_DECODER_UNSPECIFIED, nil
	case DecoderStandard:
		return composerpb.Decoder_DECODER_STANDARD, nil
	case DecoderLegacy:
		return composerpb.Decoder_DECODER_LEGACY, nil
	default:
		return 0, invalidArgument("composer: unknown decoder " + string(d))
	}
}

func decoderFromProto(d composerpb.Decoder) Decoder {
	switch d {
	case composerpb.Decoder_DECODER_UNSPECIFIED:
		return DecoderDefault
	case composerpb.Decoder_DECODER_STANDARD:
		return DecoderStandard
	case composerpb.Decoder_DECODER_LEGACY:
		return DecoderLegacy
	default:
		return Decoder(d.String())
	}
}

// Sampling overrides one autoregressive stage's sampling. Nil fields keep
// the model's defaults (Capabilities reports them). Raise MaxTokens when a
// result reports truncation; the server refuses the request when the budget
// no longer fits the model's context window.
type Sampling struct {
	Temperature       *float64 // 0–5
	TopP              *float64 // (0, 1]
	TopK              *int     // >= 1
	RepetitionPenalty *float64 // > 0
	PenaltyWindow     *int     // 1–100
	MinTokens         *int     // >= 0
	MaxTokens         *int     // >= 1
}

func (s *Sampling) toProto() *composerpb.SamplingOverrides {
	if s == nil {
		return nil
	}

	return &composerpb.SamplingOverrides{
		Temperature:       s.Temperature,
		TopP:              s.TopP,
		TopK:              int32Ptr(s.TopK),
		RepetitionPenalty: s.RepetitionPenalty,
		PenaltyWindow:     int32Ptr(s.PenaltyWindow),
		MinTokens:         int32Ptr(s.MinTokens),
		MaxTokens:         int32Ptr(s.MaxTokens),
	}
}

func int32Ptr(p *int) *int32 {
	if p == nil {
		return nil
	}

	v := int32(*p) //nolint:gosec // sampling counts fit int32

	return &v
}

// SamplingDefaults is the model's effective sampling for one stage, so a
// caller can build override forms without hard-coding the model's numbers.
type SamplingDefaults struct {
	Temperature       float64
	TopP              float64
	TopK              int
	RepetitionPenalty float64
	PenaltyWindow     int
	MinTokens         int
	MaxTokens         int
}

func samplingDefaultsFromProto(d *composerpb.SamplingDefaults) SamplingDefaults {
	return SamplingDefaults{
		Temperature:       d.GetTemperature(),
		TopP:              d.GetTopP(),
		TopK:              int(d.GetTopK()),
		RepetitionPenalty: d.GetRepetitionPenalty(),
		PenaltyWindow:     int(d.GetPenaltyWindow()),
		MinTokens:         int(d.GetMinTokens()),
		MaxTokens:         int(d.GetMaxTokens()),
	}
}

// Stage names one phase of a generation.
type Stage string

const (
	StageUnspecified  Stage = ""
	StageQueued       Stage = "queued"
	StageLoading      Stage = "loading"
	StagePlanning     Stage = "planning"
	StageGenerating   Stage = "generating"
	StageSynthesizing Stage = "synthesizing"
	StageDecoding     Stage = "decoding"
	StageTranscribing Stage = "transcribing"
)

var stageFromProto = map[composerpb.ComposeProgress_Stage]Stage{
	composerpb.ComposeProgress_STAGE_UNSPECIFIED:  StageUnspecified,
	composerpb.ComposeProgress_STAGE_QUEUED:       StageQueued,
	composerpb.ComposeProgress_STAGE_LOADING:      StageLoading,
	composerpb.ComposeProgress_STAGE_PLANNING:     StagePlanning,
	composerpb.ComposeProgress_STAGE_GENERATING:   StageGenerating,
	composerpb.ComposeProgress_STAGE_SYNTHESIZING: StageSynthesizing,
	composerpb.ComposeProgress_STAGE_DECODING:     StageDecoding,
	composerpb.ComposeProgress_STAGE_TRANSCRIBING: StageTranscribing,
}

// Progress is one periodic update from a running generation. Total is zero
// while a stage cannot know its length — the token stages end when the model
// decides the song is over.
type Progress struct {
	Stage     Stage
	Completed int64
	Total     int64
	// QueuePosition is how many generations run or wait ahead of this one.
	QueuePosition int
	// Candidate is the take currently rendering. Nil outside a
	// multi-candidate song's per-candidate stages.
	Candidate *int
}

func progressFromProto(p *composerpb.ComposeProgress) Progress {
	progress := Progress{
		Stage:         stageFromProto[p.GetStage()],
		Completed:     p.GetCompleted(),
		Total:         p.GetTotal(),
		QueuePosition: int(p.GetQueuePosition()),
	}
	if progress.Stage == "" {
		progress.Stage = Stage(p.GetStage().String())
	}

	if p.Candidate != nil {
		candidate := int(p.GetCandidate())
		progress.Candidate = &candidate
	}

	return progress
}

// PlanOptions tunes Plan. The zero value plans melody and chords with a
// server-chosen seed.
type PlanOptions struct {
	// Cot selects what is planned. CotModeOff is not valid here — a plan is
	// the score.
	Cot CotMode

	// Seed makes planning deterministic against one model revision. Nil:
	// server-chosen, reported back in the result.
	Seed *int64

	// Instrumental moves every vocal note into the instrumental part and
	// allows empty lyrics. Say it in the style too
	// ("Instrumental, ..., no vocals").
	Instrumental bool

	// ABC is a score to convert instead of planning one. Only meaningful
	// with Instrumental — a plain plan of a provided score is a no-op.
	ABC string

	// ABCSampling overrides the score-planning sampling.
	ABCSampling *Sampling

	// OnProgress is called for every progress frame, on the consuming
	// goroutine.
	OnProgress func(Progress)
}

// SongOptions tunes ComposeSong. The zero value renders one take from a full
// plan with a server-chosen seed and the standard decoder.
type SongOptions struct {
	Cot  CotMode
	Seed *int64

	// ABC is a score to follow instead of planning one — typically a Plan
	// result (or a Transcribe result for a cover), edited. Needs cot full
	// or melody.
	ABC string

	// CFGScale is the classifier-free guidance, 0–20. Nil: model default.
	CFGScale *float64

	Instrumental bool

	ABCSampling      *Sampling
	SemanticSampling *Sampling

	// Candidates is how many takes to render from one plan, with seeds
	// seed..seed+N-1. Zero means one. Each take costs a full generation;
	// the ceiling is Capabilities.MaxCandidates.
	Candidates int

	Decoder Decoder

	// OnProgress is called for every progress frame, on the consuming
	// goroutine. Progress.Candidate names the take during per-take stages.
	OnProgress func(Progress)
}

// spec converts the options into the shared wire spec.
func (o *SongOptions) spec(style, lyrics string) (*composerpb.SongSpec, error) {
	cotProto, err := o.Cot.toProto()
	if err != nil {
		return nil, err
	}

	spec := &composerpb.SongSpec{
		Style:            style,
		Lyrics:           lyrics,
		Cot:              cotProto,
		Seed:             o.Seed,
		CfgScale:         o.CFGScale,
		Instrumental:     o.Instrumental,
		AbcSampling:      o.ABCSampling.toProto(),
		SemanticSampling: o.SemanticSampling.toProto(),
	}
	if o.ABC != "" {
		abc := o.ABC
		spec.Abc = &abc
	}

	return spec, nil
}

// song views the plan options as song options, for the shared spec encoding.
func (o *PlanOptions) song() *SongOptions {
	return &SongOptions{
		Cot:          o.Cot,
		Seed:         o.Seed,
		ABC:          o.ABC,
		Instrumental: o.Instrumental,
		ABCSampling:  o.ABCSampling,
	}
}

// Plan is the editable score, before any audio exists. Submit ABC back
// through SongOptions.ABC (send Seed along to keep everything else as it
// was).
type Plan struct {
	Seed int64
	Cot  CotMode

	// ABC is the score to edit and resubmit. With Instrumental it is the
	// converted score; the untouched plan is ABCOriginal.
	ABC         string
	ABCOriginal string

	// Truncated reports that the plan hit its token budget and is
	// incomplete. Retry with a larger ABCSampling.MaxTokens.
	Truncated bool
}

func planFromProto(p *composerpb.PlanResult) *Plan {
	return &Plan{
		Seed:        p.GetSeed(),
		Cot:         cotModeFromProto(p.GetCot()),
		ABC:         p.GetAbc(),
		ABCOriginal: p.GetAbcOriginal(),
		Truncated:   p.GetTruncated(),
	}
}

// Candidate is one rendered take of a song.
type Candidate struct {
	// Index matches the order in Song.Candidates.
	Index int
	Seed  int64

	Duration time.Duration

	// Truncated reports that this take hit its token budget and ends early.
	// Retry with a larger SemanticSampling.MaxTokens.
	Truncated bool

	// Audio is the take as 48 kHz stereo FLAC.
	Audio rootpullersdk.File
}

// Song is a finished generation: the score it followed and every rendered
// take. Picking the best take is the caller's job.
type Song struct {
	Seed int64
	Cot  CotMode

	// ABC is the score the song followed (converted when instrumental);
	// empty with CotModeOff.
	ABC         string
	ABCOriginal string

	// PlanTruncated reports that the plan hit its token budget; the takes'
	// own truncation is per candidate.
	PlanTruncated bool

	Decoder        Decoder
	Candidates     []Candidate
	ProcessingTime time.Duration
}

// Transcription is a recording as an editable score in the model's native
// ABC dialect: vocal and instrumental staves, chords unless MelodyOnly. Hand
// ABC to ComposeSong (cot melody for a new style, full to keep the harmony;
// Instrumental for an instrumental cover).
type Transcription struct {
	ABC        string
	MelodyOnly bool
}

// TranscribeOptions tunes Transcribe. The zero value transcribes the melody
// only.
type TranscribeOptions struct {
	// MelodyOnly omits the chord symbols — the right input for a cover that
	// changes style. Nil keeps the server default (true).
	MelodyOnly *bool

	// OnProgress is called for every progress frame, on the consuming
	// goroutine.
	OnProgress func(Progress)
}

// Capabilities describes what a deployment serves and the limits to design
// around. Fetch once at startup; refresh on reconnect.
type Capabilities struct {
	Model         string
	ModelRevision string

	// Decoders this deployment can render with. DecoderLegacy appears only
	// when its weights are downloaded.
	Decoders []Decoder

	// Transcription reports whether Transcribe is served. The SheetSage2
	// sidecar is optional equipment; without it Transcribe fails with
	// FAILED_PRECONDITION.
	Transcription bool

	MaxCandidates int

	ABCSamplingDefaults      SamplingDefaults
	SemanticSamplingDefaults SamplingDefaults

	SampleRate int
	Channels   int

	MaxStyleChars  int
	MaxLyricsChars int
	MaxABCChars    int

	// MaxAudioBytes is the Transcribe upload cap.
	MaxAudioBytes int64
}

func capabilitiesFromProto(c *composerpb.Capabilities) *Capabilities {
	decoders := make([]Decoder, 0, len(c.GetDecoders()))
	for _, d := range c.GetDecoders() {
		decoders = append(decoders, decoderFromProto(d))
	}

	return &Capabilities{
		Model:                    c.GetModel(),
		ModelRevision:            c.GetModelRevision(),
		Decoders:                 decoders,
		Transcription:            c.GetTranscription(),
		MaxCandidates:            int(c.GetMaxCandidates()),
		ABCSamplingDefaults:      samplingDefaultsFromProto(c.GetAbcSamplingDefaults()),
		SemanticSamplingDefaults: samplingDefaultsFromProto(c.GetSemanticSamplingDefaults()),
		SampleRate:               int(c.GetSampleRate()),
		Channels:                 int(c.GetChannels()),
		MaxStyleChars:            int(c.GetMaxStyleChars()),
		MaxLyricsChars:           int(c.GetMaxLyricsChars()),
		MaxABCChars:              int(c.GetMaxAbcChars()),
		MaxAudioBytes:            c.GetMaxAudioBytes(),
	}
}
