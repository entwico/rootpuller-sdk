package video

import (
	"fmt"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	videopb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/video"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// Models the server runs on its ComfyUI deployments. A deployment serves a
// subset; asking for a model it does not serve fails with NotFound.
const (
	// ModelLTX25 renders clips of 1-6 s at 24 fps with sound, from text, a
	// first frame, or a first and a last frame. The default of Generate.
	ModelLTX25 = "ltx-2.5"
	// ModelInfiniteTalk lip-syncs a portrait to a voice track. The default
	// of GenerateTalking.
	ModelInfiniteTalk = "infinitetalk"
)

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// GenerateOptions shapes a clip. Every field is optional.
type GenerateOptions struct {
	// Model defaults to ModelLTX25.
	Model string

	// FirstFrame is an image the clip starts from (image-to-video).
	// LastFrame is an image it ends on; it requires FirstFrame. PNG, JPEG
	// or WebP, up to 32 MiB; scaled and centre-cropped to the clip size.
	FirstFrame *rootpullersdk.Upload
	LastFrame  *rootpullersdk.Upload

	// NegativePrompt lists what must not appear, e.g. "text, watermark".
	NegativePrompt *string

	// FrameCount is 8n+1 between 9 and 145 at 24 fps: 97 is 4 s, 121 is
	// 5 s, 145 is 6 s. Default: 97 from text, 145 with frames.
	FrameCount *int

	// Width and Height are multiples of 64, 256-1280. Default: 1024x576
	// from text, 1152x768 with frames.
	Width  *int
	Height *int

	// Seed defaults to random; the used seed comes back in Metadata.Seed.
	Seed *int64

	// OnProgress, when set, receives progress frames as the server reports
	// them. Called synchronously from the receive loop.
	OnProgress func(Progress)
}

func (o *GenerateOptions) toProto(prompt string) (*videopb.GenerateVideoRequest_Params, error) {
	if o.LastFrame != nil && o.FirstFrame == nil {
		return nil, invalidArgument("LastFrame requires FirstFrame")
	}

	model := o.Model
	if model == "" {
		model = ModelLTX25
	}

	return &videopb.GenerateVideoRequest_Params{
		Model:          model,
		Prompt:         prompt,
		NegativePrompt: o.NegativePrompt,
		FrameCount:     protoconv.Int32Ptr(o.FrameCount),
		Width:          protoconv.Int32Ptr(o.Width),
		Height:         protoconv.Int32Ptr(o.Height),
		Seed:           o.Seed,
	}, nil
}

// TalkingOptions shapes a talking-head video. Every field is optional.
type TalkingOptions struct {
	// Model defaults to ModelInfiniteTalk.
	Model string

	// Seed defaults to random.
	Seed *int64

	// OnProgress, when set, receives progress frames as the server reports
	// them. Called synchronously from the receive loop.
	OnProgress func(Progress)
}

func (o *TalkingOptions) toProto(prompt string) *videopb.GenerateTalkingVideoRequest_Params {
	model := o.Model
	if model == "" {
		model = ModelInfiniteTalk
	}

	return &videopb.GenerateTalkingVideoRequest_Params{Model: model, Prompt: prompt, Seed: o.Seed}
}

// Stage is the coarse state of a call.
type Stage string

const (
	StageUnspecified Stage = ""
	// StageQueued: waiting in the deployment's queue; Message says how many
	// jobs are ahead.
	StageQueued Stage = "queued"
	// StageGenerating: rendering; Message says the step (and chunk).
	StageGenerating Stage = "generating"
	// StageJoining: joining talking-head chunks and the voice track.
	StageJoining Stage = "joining"
	// StageUploading: the video is being sent back.
	StageUploading Stage = "uploading"
)

var stageFromProto = map[videopb.GenerationProgress_Stage]Stage{
	videopb.GenerationProgress_STAGE_UNSPECIFIED: StageUnspecified,
	videopb.GenerationProgress_STAGE_QUEUED:      StageQueued,
	videopb.GenerationProgress_STAGE_GENERATING:  StageGenerating,
	videopb.GenerationProgress_STAGE_JOINING:     StageJoining,
	videopb.GenerationProgress_STAGE_UPLOADING:   StageUploading,
}

// Progress is one progress frame.
type Progress struct {
	Stage Stage
	// Percentage is the overall progress 0.0-1.0, never decreasing.
	Percentage float32
	Message    string
}

func progressFromProto(p *videopb.GenerationProgress) Progress {
	return Progress{
		Stage:      enumFromProto(stageFromProto, p.GetStage()),
		Percentage: p.GetPercentage(),
		Message:    p.GetMessage(),
	}
}

// Mode is what kind of video was rendered.
type Mode string

const (
	ModeUnspecified    Mode = ""
	ModeTextToVideo    Mode = "text-to-video"
	ModeImageToVideo   Mode = "image-to-video"
	ModeFirstLastFrame Mode = "first-last-frame"
	ModeTalkingHead    Mode = "talking-head"
)

var modeFromProto = map[videopb.VideoMetadata_Mode]Mode{
	videopb.VideoMetadata_MODE_UNSPECIFIED:      ModeUnspecified,
	videopb.VideoMetadata_MODE_TEXT_TO_VIDEO:    ModeTextToVideo,
	videopb.VideoMetadata_MODE_IMAGE_TO_VIDEO:   ModeImageToVideo,
	videopb.VideoMetadata_MODE_FIRST_LAST_FRAME: ModeFirstLastFrame,
	videopb.VideoMetadata_MODE_TALKING_HEAD:     ModeTalkingHead,
}

// Metadata describes the rendered video.
type Metadata struct {
	Model string
	Mode  Mode
	// Seed is the seed actually used; send it again to get the same video.
	Seed       int64
	Size       rootpullersdk.ImageSize
	FrameCount int
	FrameRate  float32
	Duration   time.Duration
	HasAudio   bool
	// ProcessingTime includes the time spent in the deployment's queue.
	ProcessingTime time.Duration
	// Notes are free-form server remarks, e.g. "deployment: cluster".
	Notes []string
}

func metadataFromProto(m *videopb.VideoMetadata) Metadata {
	return Metadata{
		Model:          m.GetModel(),
		Mode:           enumFromProto(modeFromProto, m.GetMode()),
		Seed:           m.GetSeed(),
		Size:           rootpullersdk.ImageSize{Width: int(m.GetWidth()), Height: int(m.GetHeight())},
		FrameCount:     int(m.GetFrameCount()),
		FrameRate:      m.GetFrameRate(),
		Duration:       time.Duration(m.GetDurationMs()) * time.Millisecond,
		HasAudio:       m.GetHasAudio(),
		ProcessingTime: time.Duration(m.GetProcessingTimeMs()) * time.Millisecond,
		Notes:          m.GetNotes(),
	}
}

// Result is a rendered video ("video_0.mp4", video/mp4) and its metadata.
type Result struct {
	Video    rootpullersdk.File
	Metadata Metadata
}

// enumFromProto tolerates enum values newer than this SDK by falling back
// to the proto identifier string.
func enumFromProto[P interface {
	comparable
	fmt.Stringer
}, F ~string](m map[P]F, v P) F {
	if f, ok := m[v]; ok {
		return f
	}

	return F(v.String())
}
