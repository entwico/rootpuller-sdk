package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	videopb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/video"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/video/videoconnect"
	"github.com/entwico/rootpuller-sdk/video"
	"github.com/entwico/rootpuller-sdk/voice"
)

// Video is a facade-typed fake VideoGeneratorService. It enforces the wire
// protocol like the real server: params first and exactly once; on
// GenerateVideo no first_frame after last_frame and no last_frame without a
// first_frame; on GenerateTalkingVideo a portrait, then audio, unless the
// params carry speech, which forbids audio; chunks are capped at 2 MiB.
// Violations fail with InvalidArgument. Speech is not spoken: TalkingFunc
// sees it on the request and Audio stays empty.
//
// A nil hook responds with a canned 4-byte "video_0.mp4". Progress frames
// are streamed first, then the metadata frame (Metadata, or one synthesized
// from the request when nil), then the video, chunked like the real server.
type Video struct {
	GenerateFunc func(req VideoRequest) (rootpullersdk.File, error)
	TalkingFunc  func(req TalkingRequest) (rootpullersdk.File, error)
	Metadata     *video.Metadata
	Progress     []video.Progress
}

// VideoRequest is what the fake received on one GenerateVideo call.
type VideoRequest struct {
	Model             string
	Prompt            string
	NegativePrompt    *string
	FrameCount        *int
	Width             *int
	Height            *int
	Seed              *int64
	FirstFrame        *rootpullersdk.File
	LastFrame         *rootpullersdk.File
	ComfyUIDeployment string
}

// TalkingRequest is what the fake received on one GenerateTalkingVideo call.
// Speech is set for GenerateTalkingSpeech, Audio for GenerateTalking.
type TalkingRequest struct {
	Model             string
	Prompt            string
	Seed              *int64
	Portrait          rootpullersdk.File
	Audio             rootpullersdk.File
	Speech            *video.Speech
	ComfyUIDeployment string
}

var (
	errFirstAfterLast   = errors.New("first_frame frame after last_frame")
	errLastWithoutFirst = errors.New("last_frame without first_frame")
	errPortraitAfter    = errors.New("portrait frame after audio")
	errMissingPortrait  = errors.New("missing portrait")
	errMissingAudio     = errors.New("missing audio")
	errAudioAndSpeech   = errors.New("audio sent with speech")
	errModelAndPrompt   = errors.New("model and prompt are required")
)

func (f *Video) register(mux *http.ServeMux) {
	mux.Handle(videoconnect.NewVideoGeneratorServiceHandler(&videoHandler{fake: f}))
}

type videoHandler struct {
	fake *Video
}

func (h *videoHandler) GenerateVideo(
	_ context.Context,
	stream *connect.BidiStream[videopb.GenerateVideoRequest, videopb.GenerateVideoResponse],
) error {
	got, err := receiveVideoRequest(stream)
	if err != nil {
		return err
	}

	params, first, last := got.params, got.first, got.last

	if blank(params.GetModel()) || blank(params.GetPrompt()) {
		return invalidArgument(errModelAndPrompt)
	}

	req := VideoRequest{
		Model:             params.GetModel(),
		Prompt:            params.GetPrompt(),
		NegativePrompt:    params.NegativePrompt,
		FrameCount:        intPtr(params.FrameCount), //nolint:protogetter // presence matters
		Width:             intPtr(params.Width),      //nolint:protogetter // presence matters
		Height:            intPtr(params.Height),     //nolint:protogetter // presence matters
		Seed:              params.Seed,
		FirstFrame:        first,
		LastFrame:         last,
		ComfyUIDeployment: stream.RequestHeader().Get("Comfyui-Deployment"),
	}

	file := cannedVideo()

	if h.fake.GenerateFunc != nil {
		var err error
		if file, err = h.fake.GenerateFunc(req); err != nil {
			return err
		}
	}

	mode := video.ModeTextToVideo

	switch {
	case last != nil:
		mode = video.ModeFirstLastFrame
	case first != nil:
		mode = video.ModeImageToVideo
	}

	return videoRespond(h.fake, req.Model, mode, file,
		func(p *videopb.GenerationProgress) *videopb.GenerateVideoResponse {
			return &videopb.GenerateVideoResponse{Event: &videopb.GenerateVideoResponse_Progress{Progress: p}}
		},
		func(m *videopb.VideoMetadata) *videopb.GenerateVideoResponse {
			return &videopb.GenerateVideoResponse{Event: &videopb.GenerateVideoResponse_Metadata{Metadata: m}}
		},
		func(c *commonpb.FileChunk) *videopb.GenerateVideoResponse {
			return &videopb.GenerateVideoResponse{Event: &videopb.GenerateVideoResponse_File{File: c}}
		},
		stream.Send,
	)
}

// videoFrames is one GenerateVideo request stream, drained.
type videoFrames struct {
	params      *videopb.GenerateVideoRequest_Params
	first, last *rootpullersdk.File
}

// receiveVideoRequest drains a GenerateVideo request stream, enforcing
// params → first_frame → last_frame.
func receiveVideoRequest(
	stream *connect.BidiStream[videopb.GenerateVideoRequest, videopb.GenerateVideoResponse],
) (videoFrames, error) {
	var (
		params      *videopb.GenerateVideoRequest_Params
		first, last *rootpullersdk.File
	)

	for {
		req, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return videoFrames{}, err
		}

		switch r := req.GetRequest().(type) {
		case *videopb.GenerateVideoRequest_Params_:
			if params != nil {
				return videoFrames{}, invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *videopb.GenerateVideoRequest_FirstFrame:
			if params == nil {
				return videoFrames{}, errBeforeParams("first_frame")
			}

			if last != nil {
				return videoFrames{}, invalidArgument(errFirstAfterLast)
			}

			if err := appendFrame(&first, r.FirstFrame); err != nil {
				return videoFrames{}, err
			}
		case *videopb.GenerateVideoRequest_LastFrame:
			if params == nil {
				return videoFrames{}, errBeforeParams("last_frame")
			}

			if first == nil {
				return videoFrames{}, invalidArgument(errLastWithoutFirst)
			}

			if err := appendFrame(&last, r.LastFrame); err != nil {
				return videoFrames{}, err
			}
		default:
			return videoFrames{}, invalidArgument(errUnexpectedVariant)
		}
	}

	if params == nil {
		return videoFrames{}, invalidArgument(errMissingParams)
	}

	return videoFrames{params: params, first: first, last: last}, nil
}

func appendFrame(dst **rootpullersdk.File, chunk *commonpb.FileChunk) error {
	if *dst == nil {
		*dst = &rootpullersdk.File{}
	}

	return appendChunk(*dst, chunk)
}

// talkingFrames is one GenerateTalkingVideo request stream, drained.
type talkingFrames struct {
	params          *videopb.GenerateTalkingVideoRequest_Params
	portrait, audio rootpullersdk.File
}

// receiveTalkingRequest drains a GenerateTalkingVideo request stream,
// enforcing params → portrait → audio.
func receiveTalkingRequest(
	stream *connect.BidiStream[videopb.GenerateTalkingVideoRequest, videopb.GenerateTalkingVideoResponse],
) (talkingFrames, error) {
	var got talkingFrames

	for {
		req, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return got, nil
		}

		if err != nil {
			return got, err
		}

		switch r := req.GetRequest().(type) {
		case *videopb.GenerateTalkingVideoRequest_Params_:
			if got.params != nil {
				return got, invalidArgument(errDuplicateParamsFrame)
			}

			got.params = r.Params
		case *videopb.GenerateTalkingVideoRequest_Portrait:
			if got.params == nil {
				return got, errBeforeParams("portrait")
			}

			if len(got.audio.Data) > 0 {
				return got, invalidArgument(errPortraitAfter)
			}

			if err := appendChunk(&got.portrait, r.Portrait); err != nil {
				return got, err
			}
		case *videopb.GenerateTalkingVideoRequest_Audio:
			if got.params == nil {
				return got, errBeforeParams("audio")
			}

			if err := appendChunk(&got.audio, r.Audio); err != nil {
				return got, err
			}
		default:
			return got, invalidArgument(errUnexpectedVariant)
		}
	}
}

func (h *videoHandler) GenerateTalkingVideo(
	_ context.Context,
	stream *connect.BidiStream[videopb.GenerateTalkingVideoRequest, videopb.GenerateTalkingVideoResponse],
) error {
	got, err := receiveTalkingRequest(stream)
	if err != nil {
		return err
	}

	params, portrait, audio := got.params, got.portrait, got.audio

	switch {
	case params == nil:
		return invalidArgument(errMissingParams)
	case blank(params.GetModel()) || blank(params.GetPrompt()):
		return invalidArgument(errModelAndPrompt)
	case len(portrait.Data) == 0:
		return invalidArgument(errMissingPortrait)
	case params.GetSpeech() != nil && len(audio.Data) > 0:
		return invalidArgument(errAudioAndSpeech)
	case params.GetSpeech() == nil && len(audio.Data) == 0:
		return invalidArgument(errMissingAudio)
	}

	if s := params.GetSpeech(); s != nil {
		if err := validateSpeech(s.GetVoiceRef(), s.GetText(), s.GetLanguage()); err != nil {
			return err
		}
	}

	req := TalkingRequest{
		Model:             params.GetModel(),
		Prompt:            params.GetPrompt(),
		Seed:              params.Seed,
		Portrait:          portrait,
		Audio:             audio,
		ComfyUIDeployment: stream.RequestHeader().Get("Comfyui-Deployment"),
	}

	if s := params.GetSpeech(); s != nil {
		req.Speech = &video.Speech{Voice: voice.Ref(s.GetVoiceRef()), Text: s.GetText(), Language: s.GetLanguage()}
	}

	file := cannedVideo()

	if h.fake.TalkingFunc != nil {
		var err error
		if file, err = h.fake.TalkingFunc(req); err != nil {
			return err
		}
	}

	return videoRespond(h.fake, req.Model, video.ModeTalkingHead, file,
		func(p *videopb.GenerationProgress) *videopb.GenerateTalkingVideoResponse {
			return &videopb.GenerateTalkingVideoResponse{Event: &videopb.GenerateTalkingVideoResponse_Progress{Progress: p}}
		},
		func(m *videopb.VideoMetadata) *videopb.GenerateTalkingVideoResponse {
			return &videopb.GenerateTalkingVideoResponse{Event: &videopb.GenerateTalkingVideoResponse_Metadata{Metadata: m}}
		},
		func(c *commonpb.FileChunk) *videopb.GenerateTalkingVideoResponse {
			return &videopb.GenerateTalkingVideoResponse{Event: &videopb.GenerateTalkingVideoResponse_File{File: c}}
		},
		stream.Send,
	)
}

// videoRespond streams progress, metadata and the file in server order.
func videoRespond[Resp any](
	fake *Video,
	model string,
	mode video.Mode,
	file rootpullersdk.File,
	wrapProgress func(*videopb.GenerationProgress) *Resp,
	wrapMetadata func(*videopb.VideoMetadata) *Resp,
	wrapFile func(*commonpb.FileChunk) *Resp,
	send func(*Resp) error,
) error {
	for _, p := range fake.Progress {
		msg := &videopb.GenerationProgress{Stage: videoStageToProto[p.Stage], Percentage: p.Percentage, Message: p.Message}
		if err := send(wrapProgress(msg)); err != nil {
			return err
		}
	}

	meta := video.Metadata{Model: model, Mode: mode}
	if fake.Metadata != nil {
		meta = *fake.Metadata
	}

	if err := send(wrapMetadata(videoMetadataToProto(meta))); err != nil {
		return err
	}

	return SendFileChunks(&file, func(chunk *commonpb.FileChunk) error {
		return send(wrapFile(chunk))
	})
}

func cannedVideo() rootpullersdk.File {
	return rootpullersdk.File{Name: "video_0.mp4", MIMEType: "video/mp4", Data: []byte{0, 0, 0, 0x18}}
}

var videoStageToProto = map[video.Stage]videopb.GenerationProgress_Stage{
	video.StageUnspecified: videopb.GenerationProgress_STAGE_UNSPECIFIED,
	video.StageQueued:      videopb.GenerationProgress_STAGE_QUEUED,
	video.StageGenerating:  videopb.GenerationProgress_STAGE_GENERATING,
	video.StageJoining:     videopb.GenerationProgress_STAGE_JOINING,
	video.StageUploading:   videopb.GenerationProgress_STAGE_UPLOADING,
	video.StageSpeaking:    videopb.GenerationProgress_STAGE_SPEAKING,
}

var videoModeToProto = map[video.Mode]videopb.VideoMetadata_Mode{
	video.ModeUnspecified:    videopb.VideoMetadata_MODE_UNSPECIFIED,
	video.ModeTextToVideo:    videopb.VideoMetadata_MODE_TEXT_TO_VIDEO,
	video.ModeImageToVideo:   videopb.VideoMetadata_MODE_IMAGE_TO_VIDEO,
	video.ModeFirstLastFrame: videopb.VideoMetadata_MODE_FIRST_LAST_FRAME,
	video.ModeTalkingHead:    videopb.VideoMetadata_MODE_TALKING_HEAD,
}

func videoMetadataToProto(m video.Metadata) *videopb.VideoMetadata {
	return &videopb.VideoMetadata{
		Model:            m.Model,
		Mode:             videoModeToProto[m.Mode],
		Seed:             m.Seed,
		Width:            int32(m.Size.Width),  //nolint:gosec // test fixture sizes
		Height:           int32(m.Size.Height), //nolint:gosec // test fixture sizes
		FrameCount:       int32(m.FrameCount),  //nolint:gosec // test fixture counts
		FrameRate:        m.FrameRate,
		DurationMs:       m.Duration.Milliseconds(),
		HasAudio:         m.HasAudio,
		ProcessingTimeMs: m.ProcessingTime.Milliseconds(),
		Notes:            m.Notes,
	}
}
