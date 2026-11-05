package rootpullertest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	montagepb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/montage"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/montage/montageconnect"
	"github.com/entwico/rootpuller-sdk/montage"
)

// Montage is a facade-typed fake MontageService. It enforces the wire
// protocol like the real server — the timeline first and once, then asset
// chunks named by asset id, each asset's chunks contiguous, chunks capped at
// 2 MiB — and the timeline rules that need no media probing: asset ids and
// uploads match both ways, at least one segment, ducking has a gain, a
// crossfade has a length, the explicit duration mode has a duration and the
// audio mode a track that does not loop, output size set together and even,
// fps 1-120, audio bit rate 32-320 kbps, opacity 0-1. Violations fail with
// InvalidArgument. What needs the
// media (a trim past a clip's end, an aspect mismatch) is not checked.
//
// A nil RenderFunc responds with a canned 4-byte "montage_0.mp4". Progress
// frames are streamed first, then the metadata (Metadata, or one synthesized
// from the timeline, with the server's audio: AAC 48 kHz stereo at the
// requested bit rate or the quality's), then the video, chunked like the real
// server.
// GetCapabilities answers Capabilities, or the server's defaults.
type Montage struct {
	RenderFunc   func(req MontageRequest) (rootpullersdk.File, error)
	Metadata     *montage.Metadata
	Progress     []montage.Progress
	Capabilities *montage.Capabilities
}

// MontageRequest is what the fake received on one Render call.
type MontageRequest struct {
	Timeline montage.Timeline
	// Assets maps each asset id to its bytes, name and type.
	Assets map[string]rootpullersdk.File
}

var (
	errTimelineMissing   = errors.New("missing the params frame or its timeline")
	errAssetEmpty        = errors.New("asset is empty")
	errLimitExceeded     = errors.New("limit exceeded")
	errAssetUnnamed      = errors.New("an asset's first chunk carries no id")
	errAssetInterleaved  = errors.New("asset chunks are interleaved or sent twice")
	errAssetUnreferenced = errors.New("asset is not referenced by the timeline")
	errAssetMissing      = errors.New("asset is referenced but was not uploaded")
	errTimelineInvalid   = errors.New("invalid timeline")
)

func (f *Montage) register(mux *http.ServeMux) {
	mux.Handle(montageconnect.NewMontageServiceHandler(&montageHandler{fake: f}))
}

type montageHandler struct {
	fake *Montage
}

func (h *montageHandler) Render(
	_ context.Context,
	stream *connect.BidiStream[montagepb.RenderRequest, montagepb.RenderResponse],
) error {
	timeline, assets, err := receiveMontage(stream)
	if err != nil {
		return err
	}

	caps := h.fake.capabilities()

	if err := checkTimeline(timeline, caps); err != nil {
		return invalidArgument(err)
	}

	if err := checkMontageAssets(assets, caps); err != nil {
		return invalidArgument(err)
	}

	req := MontageRequest{Timeline: timelineFromProto(timeline), Assets: map[string]rootpullersdk.File{}}
	for _, a := range assets {
		req.Assets[a.id] = a.file
	}

	file := rootpullersdk.File{Name: "montage_0.mp4", MIMEType: mimeVideoMP4, Data: []byte{0, 0, 0, 0x18}}

	if h.fake.RenderFunc != nil {
		if file, err = h.fake.RenderFunc(req); err != nil {
			return err
		}
	}

	for _, p := range h.fake.Progress {
		frame := &montagepb.Progress{Stage: montageStageToProto[p.Stage], Percentage: p.Percentage, Message: p.Message}
		if err := stream.Send(&montagepb.RenderResponse{Event: &montagepb.RenderResponse_Progress{Progress: frame}}); err != nil {
			return err
		}
	}

	meta := synthesizeMontageMetadata(req.Timeline)
	if h.fake.Metadata != nil {
		meta = *h.fake.Metadata
	}

	metaFrame := &montagepb.RenderMetadata{
		DurationMillis: meta.Duration.Milliseconds(),
		Width:          int32(meta.Width),  //nolint:gosec // test sizes fit an int32
		Height:         int32(meta.Height), //nolint:gosec // test sizes fit an int32
		Fps:            meta.FPS,

		AudioSampleRateHz: int32(meta.AudioSampleRate), //nolint:gosec // test rates fit an int32
		AudioChannels:     int32(meta.AudioChannels),   //nolint:gosec // test counts fit an int32
		AudioBitrateKbps:  int32(meta.AudioBitrate),    //nolint:gosec // test rates fit an int32
	}
	if err := stream.Send(&montagepb.RenderResponse{Event: &montagepb.RenderResponse_Metadata{Metadata: metaFrame}}); err != nil {
		return err
	}

	return SendFileChunks(&file, func(chunk *commonpb.FileChunk) error {
		return stream.Send(&montagepb.RenderResponse{Event: &montagepb.RenderResponse_File{File: chunk}})
	})
}

func (h *montageHandler) GetCapabilities(
	context.Context,
	*connect.Request[emptypb.Empty],
) (*connect.Response[montagepb.Capabilities], error) {
	caps := h.fake.capabilities()

	return connect.NewResponse(&montagepb.Capabilities{
		MaxAssets:       int32(caps.MaxAssets), //nolint:gosec // test sizes fit an int32
		MaxTotalBytes:   caps.MaxTotalBytes,
		MaxAssetBytes:   caps.MaxAssetBytes,
		MaxSegments:     int32(caps.MaxSegments),    //nolint:gosec // test sizes fit an int32
		MaxAudioTracks:  int32(caps.MaxAudioTracks), //nolint:gosec // test sizes fit an int32
		MaxOverlays:     int32(caps.MaxOverlays),    //nolint:gosec // test sizes fit an int32
		MaxDimension:    int32(caps.MaxDimension),   //nolint:gosec // test sizes fit an int32
		MaxOutputMillis: caps.MaxOutput.Milliseconds(),
		VideoMimeTypes:  caps.VideoMIMETypes,
		AudioMimeTypes:  caps.AudioMIMETypes,
		ImageMimeTypes:  caps.ImageMIMETypes,
	}), nil
}

type montageAsset struct {
	id   string
	file rootpullersdk.File
}

// receiveMontage drains a Render request stream, enforcing the timeline
// first, then contiguous asset chunks that the timeline references.
func receiveMontage(
	stream *connect.BidiStream[montagepb.RenderRequest, montagepb.RenderResponse],
) (*montagepb.Timeline, []montageAsset, error) {
	var (
		timeline *montagepb.Timeline
		assets   []montageAsset
	)

	for {
		req, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, nil, err
		}

		switch r := req.GetRequest().(type) {
		case *montagepb.RenderRequest_Params_:
			if timeline != nil {
				return nil, nil, invalidArgument(errDuplicateParamsFrame)
			}

			if timeline = r.Params.GetTimeline(); timeline == nil {
				return nil, nil, invalidArgument(errTimelineMissing)
			}
		case *montagepb.RenderRequest_Asset:
			if timeline == nil {
				return nil, nil, errBeforeParams("asset")
			}

			if err := appendMontageAsset(&assets, r.Asset, timeline); err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, invalidArgument(errUnexpectedVariant)
		}
	}

	if timeline == nil {
		return nil, nil, invalidArgument(errTimelineMissing)
	}

	for _, id := range referencedProtoAssets(timeline) {
		if !slices.ContainsFunc(assets, func(a montageAsset) bool { return a.id == id }) {
			return nil, nil, invalidArgument(fmt.Errorf("%w: %q", errAssetMissing, id))
		}
	}

	return timeline, assets, nil
}

func appendMontageAsset(assets *[]montageAsset, chunk *commonpb.FileChunk, timeline *montagepb.Timeline) error {
	id := chunk.GetName()
	last := len(*assets) - 1

	if last < 0 || (id != "" && id != (*assets)[last].id) {
		switch {
		case id == "":
			return invalidArgument(errAssetUnnamed)
		case !slices.Contains(referencedProtoAssets(timeline), id):
			return invalidArgument(fmt.Errorf("%w: %q", errAssetUnreferenced, id))
		case slices.ContainsFunc(*assets, func(a montageAsset) bool { return a.id == id }):
			return invalidArgument(fmt.Errorf("%w: %q", errAssetInterleaved, id))
		}

		*assets = append(*assets, montageAsset{id: id})
		last++
	}

	return appendChunk(&(*assets)[last].file, chunk)
}

func referencedProtoAssets(t *montagepb.Timeline) []string {
	var ids []string

	add := func(id string) {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	for _, s := range t.GetSegments() {
		add(s.GetAsset())
	}

	for _, a := range t.GetAudioTracks() {
		add(a.GetAsset())
	}

	for _, o := range t.GetOverlays() {
		add(o.GetAsset())
	}

	return ids
}

// capabilities are the configured Capabilities, or the server's defaults.
func (f *Montage) capabilities() montage.Capabilities {
	if f.Capabilities != nil {
		return *f.Capabilities
	}

	return montage.Capabilities{
		MaxAssets:      24,
		MaxTotalBytes:  512 << 20,
		MaxAssetBytes:  256 << 20,
		MaxOutput:      10 * time.Minute,
		MaxSegments:    200,
		MaxAudioTracks: 32,
		MaxOverlays:    32,
		MaxDimension:   3840,
		VideoMIMETypes: []string{mimeVideoMP4, "video/quicktime", "video/webm"},
		AudioMIMETypes: []string{"audio/mpeg", "audio/mp4", "audio/aac", "audio/wav", "audio/ogg", "audio/flac"},
		ImageMIMETypes: []string{"image/png", "image/jpeg", "image/webp"},
	}
}

// checkMontageAssets applies the server's upload rules: no empty asset, and
// the count and size limits.
func checkMontageAssets(assets []montageAsset, caps montage.Capabilities) error {
	if caps.MaxAssets > 0 && len(assets) > caps.MaxAssets {
		return fmt.Errorf("%w: %d assets, at most %d", errLimitExceeded, len(assets), caps.MaxAssets)
	}

	total := int64(0)

	for _, a := range assets {
		size := int64(len(a.file.Data))
		if size == 0 {
			return fmt.Errorf("%w: %q", errAssetEmpty, a.id)
		}

		if caps.MaxAssetBytes > 0 && size > caps.MaxAssetBytes {
			return fmt.Errorf("%w: asset %q is %d bytes, at most %d", errLimitExceeded, a.id, size, caps.MaxAssetBytes)
		}

		total += size
	}

	if caps.MaxTotalBytes > 0 && total > caps.MaxTotalBytes {
		return fmt.Errorf("%w: assets are %d bytes, at most %d", errLimitExceeded, total, caps.MaxTotalBytes)
	}

	return nil
}

func checkTracks(tracks []*montagepb.AudioTrack) error {
	for i, a := range tracks {
		switch {
		case a.GetAtMillis() < 0 || a.GetFadeInMillis() < 0 || a.GetFadeOutMillis() < 0:
			return fmt.Errorf("%w: audio track %d: at_millis and fades must not be negative", errTimelineInvalid, i)
		case !finite(a.GetGainDb()):
			return fmt.Errorf("%w: audio track %d: gain_db must be a finite number", errTimelineInvalid, i)
		}
	}

	return nil
}

func checkOverlays(overlays []*montagepb.Overlay, caps montage.Capabilities) error {
	for i, o := range overlays {
		switch {
		case o.GetAtMillis() < 0 || o.GetMarginX() < 0 || o.GetMarginY() < 0 || o.GetFadeInMillis() < 0 || o.GetFadeOutMillis() < 0:
			return fmt.Errorf("%w: overlay %d: at_millis, margins and fades must not be negative", errTimelineInvalid, i)
		case o.Width != nil && (o.GetWidth() <= 0 || (caps.MaxDimension > 0 && int(o.GetWidth()) > caps.MaxDimension)):
			return fmt.Errorf("%w: overlay %d: width must be within 1-%d", errLimitExceeded, i, caps.MaxDimension)
		case !finite(o.GetOpacity()) || o.GetOpacity() < 0 || o.GetOpacity() > 1:
			return fmt.Errorf("%w: overlay %d: opacity must be within 0.0-1.0", errTimelineInvalid, i)
		}
	}

	return nil
}

func finite(v float32) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
}

// checkOutput applies the output rules; an unset output is all defaults.
func checkOutput(out *montagepb.Output, caps montage.Capabilities) error {
	if out == nil {
		return nil
	}

	if (out.Width == nil) != (out.Height == nil) {
		return fmt.Errorf("%w: output width and height are set together", errTimelineInvalid)
	}

	if out.Width != nil && (out.GetWidth()%2 != 0 || out.GetHeight()%2 != 0 || out.GetWidth() <= 0 || out.GetHeight() <= 0) {
		return fmt.Errorf("%w: output width and height must be positive and even", errTimelineInvalid)
	}

	if caps.MaxDimension > 0 && (int(out.GetWidth()) > caps.MaxDimension || int(out.GetHeight()) > caps.MaxDimension) {
		return fmt.Errorf("%w: output is larger than %d pixels", errLimitExceeded, caps.MaxDimension)
	}

	if out.Fps != nil && (out.GetFps() < 1 || out.GetFps() > 120) {
		return fmt.Errorf("%w: output fps must be within 1-120", errTimelineInvalid)
	}

	if out.AudioBitrateKbps != nil && (out.GetAudioBitrateKbps() < 32 || out.GetAudioBitrateKbps() > 320) {
		return fmt.Errorf("%w: output audio_bitrate_kbps must be within 32-320", errTimelineInvalid)
	}

	if out.GetFadeInMillis() < 0 || out.GetFadeOutMillis() < 0 {
		return fmt.Errorf("%w: output fades must not be negative", errTimelineInvalid)
	}

	return nil
}

func optFloat(v *float32) *float64 {
	if v == nil {
		return nil
	}

	return new(float64(*v))
}

// checkTimeline applies the server's rules that need no media.
func checkTimeline(t *montagepb.Timeline, caps montage.Capabilities) error {
	switch {
	case caps.MaxSegments > 0 && len(t.GetSegments()) > caps.MaxSegments:
		return fmt.Errorf("%w: %d segments, at most %d", errLimitExceeded, len(t.GetSegments()), caps.MaxSegments)
	case caps.MaxAudioTracks > 0 && len(t.GetAudioTracks()) > caps.MaxAudioTracks:
		return fmt.Errorf("%w: %d audio tracks, at most %d", errLimitExceeded, len(t.GetAudioTracks()), caps.MaxAudioTracks)
	case caps.MaxOverlays > 0 && len(t.GetOverlays()) > caps.MaxOverlays:
		return fmt.Errorf("%w: %d overlays, at most %d", errLimitExceeded, len(t.GetOverlays()), caps.MaxOverlays)
	}

	if len(t.GetSegments()) == 0 {
		return fmt.Errorf("%w: no segments", errTimelineInvalid)
	}

	for i, s := range t.GetSegments() {
		duck := s.GetSound() == montagepb.Segment_SOUND_DUCK

		switch {
		case s.GetAsset() == "":
			return fmt.Errorf("%w: segment %d has no asset", errTimelineInvalid, i)
		case duck != (s.GainDb != nil):
			return fmt.Errorf("%w: segment %d: gain_db goes with SOUND_DUCK and only with it", errTimelineInvalid, i)
		case !finite(s.GetGainDb()):
			return fmt.Errorf("%w: segment %d: gain_db must be a finite number", errTimelineInvalid, i)
		case s.GetTransition().GetKind() == montagepb.Transition_KIND_CROSSFADE && s.GetTransition().GetMillis() <= 0:
			return fmt.Errorf("%w: segment %d: a crossfade needs positive millis", errTimelineInvalid, i)
		}
	}

	if err := checkTracks(t.GetAudioTracks()); err != nil {
		return err
	}

	if err := checkOverlays(t.GetOverlays(), caps); err != nil {
		return err
	}

	if err := checkOutput(t.GetOutput(), caps); err != nil {
		return err
	}

	explicit := t.GetDurationMode() == montagepb.DurationMode_DURATION_MODE_EXPLICIT
	if explicit != (t.DurationMillis != nil) {
		return fmt.Errorf("%w: duration_millis goes with DURATION_MODE_EXPLICIT and only with it", errTimelineInvalid)
	}

	if t.GetDurationMode() == montagepb.DurationMode_DURATION_MODE_AUDIO &&
		!slices.ContainsFunc(t.GetAudioTracks(), func(a *montagepb.AudioTrack) bool { return !a.GetLoop() }) {
		return fmt.Errorf("%w: DURATION_MODE_AUDIO needs an audio track that does not loop", errTimelineInvalid)
	}

	return nil
}

// synthesizeMontageMetadata estimates what the server would report: the
// explicit duration, or the segments' summed durations less the crossfades.
func synthesizeMontageMetadata(t montage.Timeline) montage.Metadata {
	meta := montage.Metadata{
		Width: t.Output.Width, Height: t.Output.Height, FPS: t.Output.FPS, Duration: t.Duration,
		AudioSampleRate: 48000, AudioChannels: 2, AudioBitrate: 128,
	}

	if t.Output.Quality == montage.QualityHigh {
		meta.AudioBitrate = 192
	}

	if t.Output.AudioBitrate > 0 {
		meta.AudioBitrate = t.Output.AudioBitrate
	}

	if t.DurationMode == montage.DurationExplicit {
		return meta
	}

	for i, s := range t.Segments {
		meta.Duration += s.Duration

		if i < len(t.Segments)-1 && s.Transition.Kind == montage.TransitionCrossfade {
			meta.Duration -= s.Transition.Duration
		}
	}

	return meta
}

func timelineFromProto(t *montagepb.Timeline) montage.Timeline {
	out := montage.Timeline{
		DurationMode: montageDurationModeFromProto[t.GetDurationMode()],
		Duration:     ms(t.GetDurationMillis()),
		Output: montage.Output{
			Width:   int(t.GetOutput().GetWidth()),
			Height:  int(t.GetOutput().GetHeight()),
			FPS:     t.GetOutput().GetFps(),
			Quality: montageQualityFromProto[t.GetOutput().GetQuality()],
			FadeIn:  ms(int64(t.GetOutput().GetFadeInMillis())),
			FadeOut: ms(int64(t.GetOutput().GetFadeOutMillis())),

			AudioBitrate: int(t.GetOutput().GetAudioBitrateKbps()),
		},
	}

	for _, s := range t.GetSegments() {
		out.Segments = append(out.Segments, montage.Segment{
			Asset:    s.GetAsset(),
			Start:    ms(s.GetStartMillis()),
			Duration: ms(s.GetDurationMillis()),
			Sound:    montageSoundFromProto[s.GetSound()],
			GainDB:   optFloat(s.GainDb),
			Transition: montage.Transition{
				Kind:     montageTransitionFromProto[s.GetTransition().GetKind()],
				Duration: ms(int64(s.GetTransition().GetMillis())),
			},
		})
	}

	for _, a := range t.GetAudioTracks() {
		out.AudioTracks = append(out.AudioTracks, montage.AudioTrack{
			Asset:    a.GetAsset(),
			At:       ms(a.GetAtMillis()),
			Start:    ms(a.GetStartMillis()),
			Duration: ms(a.GetDurationMillis()),
			GainDB:   float64(a.GetGainDb()),
			FadeIn:   ms(int64(a.GetFadeInMillis())),
			FadeOut:  ms(int64(a.GetFadeOutMillis())),
			Loop:     a.GetLoop(),
		})
	}

	for _, o := range t.GetOverlays() {
		overlay := montage.Overlay{
			Asset:    o.GetAsset(),
			At:       ms(o.GetAtMillis()),
			Duration: ms(o.GetDurationMillis()),
			Anchor:   montageAnchorFromProto[o.GetAnchor()],
			MarginX:  int(o.GetMarginX()),
			MarginY:  int(o.GetMarginY()),
			Width:    int(o.GetWidth()),
			FadeIn:   ms(int64(o.GetFadeInMillis())),
			FadeOut:  ms(int64(o.GetFadeOutMillis())),
		}

		if o.Opacity != nil {
			overlay.Opacity = new(float64(o.GetOpacity()))
		}

		out.Overlays = append(out.Overlays, overlay)
	}

	return out
}

func ms(v int64) time.Duration {
	return time.Duration(v) * time.Millisecond
}

var (
	montageStageToProto = map[montage.Stage]montagepb.Progress_Stage{
		montage.StageUnspecified: montagepb.Progress_STAGE_UNSPECIFIED,
		montage.StageQueued:      montagepb.Progress_STAGE_QUEUED,
		montage.StageRendering:   montagepb.Progress_STAGE_RENDERING,
		montage.StageUploading:   montagepb.Progress_STAGE_UPLOADING,
	}
	montageSoundFromProto = map[montagepb.Segment_Sound]montage.Sound{
		montagepb.Segment_SOUND_UNSPECIFIED: montage.SoundKeep,
		montagepb.Segment_SOUND_KEEP:        montage.SoundKeep,
		montagepb.Segment_SOUND_DUCK:        montage.SoundDuck,
		montagepb.Segment_SOUND_MUTE:        montage.SoundMute,
	}
	montageTransitionFromProto = map[montagepb.Transition_Kind]montage.TransitionKind{
		montagepb.Transition_KIND_UNSPECIFIED: montage.TransitionCut,
		montagepb.Transition_KIND_CUT:         montage.TransitionCut,
		montagepb.Transition_KIND_CROSSFADE:   montage.TransitionCrossfade,
	}
	montageAnchorFromProto = map[montagepb.Overlay_Anchor]montage.Anchor{
		montagepb.Overlay_ANCHOR_UNSPECIFIED:  montage.AnchorCenter,
		montagepb.Overlay_ANCHOR_CENTER:       montage.AnchorCenter,
		montagepb.Overlay_ANCHOR_TOP_LEFT:     montage.AnchorTopLeft,
		montagepb.Overlay_ANCHOR_TOP:          montage.AnchorTop,
		montagepb.Overlay_ANCHOR_TOP_RIGHT:    montage.AnchorTopRight,
		montagepb.Overlay_ANCHOR_LEFT:         montage.AnchorLeft,
		montagepb.Overlay_ANCHOR_RIGHT:        montage.AnchorRight,
		montagepb.Overlay_ANCHOR_BOTTOM_LEFT:  montage.AnchorBottomLeft,
		montagepb.Overlay_ANCHOR_BOTTOM:       montage.AnchorBottom,
		montagepb.Overlay_ANCHOR_BOTTOM_RIGHT: montage.AnchorBottomRight,
	}
	montageQualityFromProto = map[montagepb.Output_Quality]montage.Quality{
		montagepb.Output_QUALITY_UNSPECIFIED: montage.QualityStandard,
		montagepb.Output_QUALITY_STANDARD:    montage.QualityStandard,
		montagepb.Output_QUALITY_HIGH:        montage.QualityHigh,
	}
	montageDurationModeFromProto = map[montagepb.DurationMode]montage.DurationMode{
		montagepb.DurationMode_DURATION_MODE_UNSPECIFIED: montage.DurationVideo,
		montagepb.DurationMode_DURATION_MODE_VIDEO:       montage.DurationVideo,
		montagepb.DurationMode_DURATION_MODE_AUDIO:       montage.DurationAudio,
		montagepb.DurationMode_DURATION_MODE_EXPLICIT:    montage.DurationExplicit,
	}
)
