package montage

import (
	"fmt"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	montagepb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/montage"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// Timeline is the whole edit: a video track of segments, audio laid over it,
// and images composited on top. Every asset id it names must be passed to
// Render.
type Timeline struct {
	// Segments is the video track, played in order. Required.
	Segments []Segment
	// AudioTracks are mixed together and with the segments' own sound.
	AudioTracks []AudioTrack
	// Overlays are composited in order, later ones on top.
	Overlays []Overlay
	Output   Output
	// DurationMode decides the output's length; the zero value ends it with
	// the video track.
	DurationMode DurationMode
	// Duration is the output's length for DurationExplicit, and unset for the
	// other modes.
	Duration time.Duration
}

// Segment is one shot: a video asset, or a still image shown for Duration.
type Segment struct {
	// Asset is the id of a video or image. Required.
	Asset string
	// Start trims the beginning of a video. Zero plays it from the start.
	Start time.Duration
	// Duration is how much of a video to play (clamped to what is left after
	// Start; zero plays to the end), or how long an image shows (required).
	Duration time.Duration
	// Sound is what happens to the segment's own audio. Images are silent.
	Sound Sound
	// GainDB attenuates the sound with SoundDuck, e.g. -18 for ambience under
	// a voice-over. Required with SoundDuck and rejected without it: nil is
	// "not given", so a forgotten gain fails instead of silently not ducking.
	GainDB *float64
	// Transition is the hand-over into the next segment; ignored on the last.
	Transition Transition
}

// Sound is what a segment does with its own audio.
type Sound string

const (
	// SoundKeep leaves it unchanged. The default.
	SoundKeep Sound = ""
	// SoundDuck attenuates it by the segment's GainDB.
	SoundDuck Sound = "duck"
	// SoundMute drops it.
	SoundMute Sound = "mute"
)

// Transition is how a segment hands over to the next.
type Transition struct {
	Kind TransitionKind
	// Duration is the crossfade's length. Both segments give up that much of
	// their own time, so every crossfade shortens the video track by it.
	Duration time.Duration
}

// TransitionKind selects the transition.
type TransitionKind string

const (
	// TransitionCut is a hard cut. The default.
	TransitionCut TransitionKind = ""
	// TransitionCrossfade blends the segments over the transition's Duration.
	TransitionCrossfade TransitionKind = "crossfade"
)

// AudioTrack is an audio asset placed on the output's clock: a voice-over, a
// music bed.
type AudioTrack struct {
	// Asset is the id of an audio file (or a video with sound). Required.
	Asset string
	// At is where on the output it starts.
	At time.Duration
	// Start and Duration trim the source; zero means its start and its end.
	Start    time.Duration
	Duration time.Duration
	// GainDB changes the volume; negative attenuates.
	GainDB  float64
	FadeIn  time.Duration
	FadeOut time.Duration
	// Loop repeats the trimmed audio until the output ends.
	Loop bool
}

// Overlay is an image composited over the video for a time range: a caption,
// a title, a logo. Render text to an image with transparency yourself.
type Overlay struct {
	// Asset is the id of an image; transparency is respected. Required.
	Asset string
	// At is when it appears.
	At time.Duration
	// Duration is how long it stays; zero keeps it to the end.
	Duration time.Duration
	Anchor   Anchor
	// MarginX and MarginY are the distance from the anchored edges in output
	// pixels; ignored on a centred axis.
	MarginX int
	MarginY int
	// Width scales the image to this many output pixels, aspect kept; zero
	// keeps its own size.
	Width int
	// Opacity is 0.0-1.0; nil means fully opaque.
	Opacity *float64
	FadeIn  time.Duration
	FadeOut time.Duration
}

// Anchor is where an overlay is pinned.
type Anchor string

const (
	AnchorCenter      Anchor = ""
	AnchorTopLeft     Anchor = "top-left"
	AnchorTop         Anchor = "top"
	AnchorTopRight    Anchor = "top-right"
	AnchorLeft        Anchor = "left"
	AnchorRight       Anchor = "right"
	AnchorBottomLeft  Anchor = "bottom-left"
	AnchorBottom      Anchor = "bottom"
	AnchorBottomRight Anchor = "bottom-right"
)

// Output is the encode target: an MP4 with H.264 video and AAC audio.
type Output struct {
	// Width and Height default to the first segment's display size and are
	// required when the first segment is an image. Set both or neither; both
	// must be even. A segment of another aspect ratio is an error.
	Width  int
	Height int
	// FPS defaults to the first video segment's rate.
	FPS     float64
	Quality Quality
	// FadeIn and FadeOut fade from and to black and silence.
	FadeIn  time.Duration
	FadeOut time.Duration
	// AudioBitrate is the AAC bit rate in kbps, 32-320, replacing the one
	// Quality sets; 0 keeps it. The audio is always 48 kHz stereo, and every
	// asset is decoded once and encoded once, here: WAV or FLAC inputs reach
	// this encode without a lossy step in between.
	AudioBitrate int
}

// Quality selects the encoder settings. Both encode H.264 video and AAC audio
// at 48 kHz stereo.
type Quality string

const (
	// QualityStandard suits social feeds, the default: x264 preset "medium"
	// at CRF 23, AAC at 128 kbps.
	QualityStandard Quality = ""
	// QualityHigh is visibly better on large screens, at larger files: x264
	// preset "slow" at CRF 18, AAC at 192 kbps.
	QualityHigh Quality = "high"
)

// DurationMode decides how long the output is.
type DurationMode string

const (
	// DurationVideo ends the output with the last segment. The default.
	DurationVideo DurationMode = ""
	// DurationAudio ends it with the last audio track that does not loop; a
	// shorter video track holds its last frame.
	DurationAudio DurationMode = "audio"
	// DurationExplicit makes it exactly Timeline.Duration long.
	DurationExplicit DurationMode = "explicit"
)

// Asset is one uploaded file, addressed by the timeline through ID.
type Asset struct {
	ID     string
	Upload rootpullersdk.Upload
}

// RenderOptions tunes Render.
type RenderOptions struct {
	// OnProgress receives progress while the server works. It must not block.
	OnProgress func(Progress)
}

// Stage is the coarse state of a render.
type Stage string

const (
	StageUnspecified Stage = ""
	// StageQueued: uploading, or waiting for a render slot.
	StageQueued Stage = "queued"
	// StageRendering: cutting, mixing and encoding.
	StageRendering Stage = "rendering"
	// StageUploading: the video is being sent back.
	StageUploading Stage = "uploading"
)

// Progress is one progress frame.
type Progress struct {
	Stage Stage
	// Percentage is the overall progress 0.0-1.0, never decreasing.
	Percentage float32
	Message    string
}

// Metadata describes the rendered video.
type Metadata struct {
	// Duration is the rendered length, after transitions and the duration
	// mode.
	Duration time.Duration
	Width    int
	Height   int
	FPS      float64

	// AudioSampleRate (Hz), AudioChannels and AudioBitrate (kbps) describe
	// the AAC track delivered.
	AudioSampleRate int
	AudioChannels   int
	AudioBitrate    int
}

// Result is a rendered montage.
type Result struct {
	// Video is the MP4.
	Video    rootpullersdk.File
	Metadata Metadata
}

// Capabilities are the server's render limits.
type Capabilities struct {
	// MaxAssets is the most assets one Render takes.
	MaxAssets int
	// MaxTotalBytes bounds the summed size of all assets, MaxAssetBytes one
	// asset's.
	MaxTotalBytes int64
	MaxAssetBytes int64
	// MaxOutput is the longest output rendered.
	MaxOutput time.Duration
	// MaxSegments, MaxAudioTracks and MaxOverlays bound each list of a
	// timeline.
	MaxSegments    int
	MaxAudioTracks int
	MaxOverlays    int
	// MaxDimension bounds the output's width and height and an overlay's
	// width, in pixels.
	MaxDimension int
	// The accepted upload types per kind, e.g. "video/mp4".
	VideoMIMETypes []string
	AudioMIMETypes []string
	ImageMIMETypes []string
}

var (
	soundToProto = map[Sound]montagepb.Segment_Sound{
		SoundKeep: montagepb.Segment_SOUND_UNSPECIFIED,
		SoundDuck: montagepb.Segment_SOUND_DUCK,
		SoundMute: montagepb.Segment_SOUND_MUTE,
	}
	transitionToProto = map[TransitionKind]montagepb.Transition_Kind{
		TransitionCut:       montagepb.Transition_KIND_UNSPECIFIED,
		TransitionCrossfade: montagepb.Transition_KIND_CROSSFADE,
	}
	anchorToProto = map[Anchor]montagepb.Overlay_Anchor{
		AnchorCenter:      montagepb.Overlay_ANCHOR_UNSPECIFIED,
		AnchorTopLeft:     montagepb.Overlay_ANCHOR_TOP_LEFT,
		AnchorTop:         montagepb.Overlay_ANCHOR_TOP,
		AnchorTopRight:    montagepb.Overlay_ANCHOR_TOP_RIGHT,
		AnchorLeft:        montagepb.Overlay_ANCHOR_LEFT,
		AnchorRight:       montagepb.Overlay_ANCHOR_RIGHT,
		AnchorBottomLeft:  montagepb.Overlay_ANCHOR_BOTTOM_LEFT,
		AnchorBottom:      montagepb.Overlay_ANCHOR_BOTTOM,
		AnchorBottomRight: montagepb.Overlay_ANCHOR_BOTTOM_RIGHT,
	}
	qualityToProto = map[Quality]montagepb.Output_Quality{
		QualityStandard: montagepb.Output_QUALITY_UNSPECIFIED,
		QualityHigh:     montagepb.Output_QUALITY_HIGH,
	}
	durationModeToProto = map[DurationMode]montagepb.DurationMode{
		DurationVideo:    montagepb.DurationMode_DURATION_MODE_UNSPECIFIED,
		DurationAudio:    montagepb.DurationMode_DURATION_MODE_AUDIO,
		DurationExplicit: montagepb.DurationMode_DURATION_MODE_EXPLICIT,
	}
	stageFromProto = map[montagepb.Progress_Stage]Stage{
		montagepb.Progress_STAGE_UNSPECIFIED: StageUnspecified,
		montagepb.Progress_STAGE_QUEUED:      StageQueued,
		montagepb.Progress_STAGE_RENDERING:   StageRendering,
		montagepb.Progress_STAGE_UPLOADING:   StageUploading,
	}
)

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// enumToProto maps a facade enum; unknown values fail locally, before any
// RPC.
func enumToProto[F ~string, P any](m map[F]P, v F, what string) (P, error) {
	p, ok := m[v]
	if !ok {
		return p, invalidArgument(fmt.Sprintf("montage: unknown %s %q", what, v))
	}

	return p, nil
}

func timelineToProto(t Timeline) (*montagepb.Timeline, error) {
	mode, err := enumToProto(durationModeToProto, t.DurationMode, "duration mode")
	if err != nil {
		return nil, err
	}

	quality, err := enumToProto(qualityToProto, t.Output.Quality, "quality")
	if err != nil {
		return nil, err
	}

	out := &montagepb.Timeline{
		DurationMode: mode,
		Output: &montagepb.Output{
			Width:         optInt32(t.Output.Width),
			Height:        optInt32(t.Output.Height),
			Quality:       quality,
			FadeInMillis:  millis32(t.Output.FadeIn),
			FadeOutMillis: millis32(t.Output.FadeOut),

			AudioBitrateKbps: optInt32(t.Output.AudioBitrate),
		},
	}

	if t.Output.FPS > 0 {
		out.Output.Fps = new(t.Output.FPS)
	}

	if t.Duration > 0 {
		out.DurationMillis = new(t.Duration.Milliseconds())
	}

	for i, s := range t.Segments {
		seg, err := segmentToProto(s)
		if err != nil {
			return nil, fmt.Errorf("segment %d: %w", i, err)
		}

		out.Segments = append(out.Segments, seg)
	}

	for _, a := range t.AudioTracks {
		track := &montagepb.AudioTrack{
			Asset:          a.Asset,
			AtMillis:       a.At.Milliseconds(),
			StartMillis:    optMillis(a.Start),
			DurationMillis: optMillis(a.Duration),
			FadeInMillis:   millis32(a.FadeIn),
			FadeOutMillis:  millis32(a.FadeOut),
			Loop:           a.Loop,
		}

		if a.GainDB != 0 {
			track.GainDb = new(float32(a.GainDB))
		}

		out.AudioTracks = append(out.AudioTracks, track)
	}

	for i, o := range t.Overlays {
		anchor, err := enumToProto(anchorToProto, o.Anchor, "anchor")
		if err != nil {
			return nil, fmt.Errorf("overlay %d: %w", i, err)
		}

		overlay := &montagepb.Overlay{
			Asset:          o.Asset,
			AtMillis:       o.At.Milliseconds(),
			DurationMillis: optMillis(o.Duration),
			Anchor:         anchor,
			MarginX:        protoconv.ClampInt32(int64(o.MarginX)),
			MarginY:        protoconv.ClampInt32(int64(o.MarginY)),
			Width:          optInt32(o.Width),
			FadeInMillis:   millis32(o.FadeIn),
			FadeOutMillis:  millis32(o.FadeOut),
		}

		if o.Opacity != nil {
			overlay.Opacity = new(float32(*o.Opacity))
		}

		out.Overlays = append(out.Overlays, overlay)
	}

	return out, nil
}

func segmentToProto(s Segment) (*montagepb.Segment, error) {
	sound, err := enumToProto(soundToProto, s.Sound, "sound")
	if err != nil {
		return nil, err
	}

	kind, err := enumToProto(transitionToProto, s.Transition.Kind, "transition")
	if err != nil {
		return nil, err
	}

	seg := &montagepb.Segment{
		Asset:          s.Asset,
		StartMillis:    optMillis(s.Start),
		DurationMillis: optMillis(s.Duration),
		Sound:          sound,
	}

	if s.GainDB != nil {
		seg.GainDb = new(float32(*s.GainDB))
	}

	if s.Transition.Kind != TransitionCut {
		seg.Transition = &montagepb.Transition{Kind: kind, Millis: new(millis32(s.Transition.Duration))}
	}

	return seg, nil
}

func progressFromProto(p *montagepb.Progress) Progress {
	stage, ok := stageFromProto[p.GetStage()]
	if !ok {
		// a stage newer than this SDK keeps its proto name.
		stage = Stage(p.GetStage().String())
	}

	return Progress{Stage: stage, Percentage: p.GetPercentage(), Message: p.GetMessage()}
}

func metadataFromProto(m *montagepb.RenderMetadata) Metadata {
	return Metadata{
		Duration: time.Duration(m.GetDurationMillis()) * time.Millisecond,
		Width:    int(m.GetWidth()),
		Height:   int(m.GetHeight()),
		FPS:      m.GetFps(),

		AudioSampleRate: int(m.GetAudioSampleRateHz()),
		AudioChannels:   int(m.GetAudioChannels()),
		AudioBitrate:    int(m.GetAudioBitrateKbps()),
	}
}

func capabilitiesFromProto(c *montagepb.Capabilities) *Capabilities {
	return &Capabilities{
		MaxAssets:      int(c.GetMaxAssets()),
		MaxTotalBytes:  c.GetMaxTotalBytes(),
		MaxAssetBytes:  c.GetMaxAssetBytes(),
		MaxOutput:      time.Duration(c.GetMaxOutputMillis()) * time.Millisecond,
		MaxSegments:    int(c.GetMaxSegments()),
		MaxAudioTracks: int(c.GetMaxAudioTracks()),
		MaxOverlays:    int(c.GetMaxOverlays()),
		MaxDimension:   int(c.GetMaxDimension()),
		VideoMIMETypes: c.GetVideoMimeTypes(),
		AudioMIMETypes: c.GetAudioMimeTypes(),
		ImageMIMETypes: c.GetImageMimeTypes(),
	}
}

// optMillis is unset for zero, which every optional duration treats as "not
// given".
func optMillis(d time.Duration) *int64 {
	if d <= 0 {
		return nil
	}

	return new(d.Milliseconds())
}

func millis32(d time.Duration) int32 {
	return protoconv.ClampInt32(d.Milliseconds())
}

func optInt32(v int) *int32 {
	if v == 0 {
		return nil
	}

	return new(protoconv.ClampInt32(int64(v)))
}
