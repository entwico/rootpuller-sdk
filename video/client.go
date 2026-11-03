// Package video wraps com.entwico.rootpuller.video.VideoGeneratorService:
// short clips with sound from text, a first frame, or a first and a last
// frame, and talking-head videos that lip-sync a portrait to a voice track.
// Both run on self-hosted models on a ComfyUI deployment.
//
// A deployment renders one job at a time, so a call can wait in its queue;
// clips take minutes, talking heads about 1 h 45 min of GPU time per minute
// of speech. The call is the job: it returns when the video is done, and
// cancelling ctx removes it from the queue or stops it. Watch progress
// through the options' OnProgress.
package video

import (
	"context"
	"iter"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	videopb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/video"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/video/videoconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the VideoGeneratorService.
type Service struct {
	rpc               videoconnect.VideoGeneratorServiceClient
	comfyUIDeployment string
}

// Option configures a Service at construction.
type Option func(*Service)

// WithComfyUIDeployment routes this service's calls at a named ComfyUI
// deployment. A per-call rootpullersdk.ContextWithComfyUIDeployment
// overrides it. Leave unset to use the server's default deployment.
func WithComfyUIDeployment(name string) Option {
	return func(s *Service) { s.comfyUIDeployment = name }
}

// NewService builds a VideoGeneratorService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: videoconnect.NewVideoGeneratorServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Generate renders one clip. Describe it like a shot: camera movement first,
// then subject, setting and light, then the sound, which the model generates
// with the picture. With opts.FirstFrame the clip starts from that image;
// with opts.LastFrame too, it moves from the first to the last.
func (s *Service) Generate(ctx context.Context, prompt string, opts *GenerateOptions) (*Result, error) {
	if opts == nil {
		opts = &GenerateOptions{}
	}

	params, err := opts.toProto(prompt)
	if err != nil {
		return nil, err
	}

	var tails []iter.Seq2[*videopb.GenerateVideoRequest, error]

	if opts.FirstFrame != nil {
		tails = append(tails, streamio.FileChunkFrames(*opts.FirstFrame, func(c *commonpb.FileChunk) *videopb.GenerateVideoRequest {
			return &videopb.GenerateVideoRequest{Request: &videopb.GenerateVideoRequest_FirstFrame{FirstFrame: c}}
		}))
	}

	if opts.LastFrame != nil {
		tails = append(tails, streamio.FileChunkFrames(*opts.LastFrame, func(c *commonpb.FileChunk) *videopb.GenerateVideoRequest {
			return &videopb.GenerateVideoRequest{Request: &videopb.GenerateVideoRequest_LastFrame{LastFrame: c}}
		}))
	}

	ctx = transport.EnsureComfyUIDeployment(ctx, s.comfyUIDeployment)
	procedure := videoconnect.VideoGeneratorServiceGenerateVideoProcedure
	stream := s.rpc.GenerateVideo(ctx)

	frames := streamio.Frames(
		&videopb.GenerateVideoRequest{Request: &videopb.GenerateVideoRequest_Params_{Params: params}},
		tails...,
	)

	var c collector

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *videopb.GenerateVideoResponse) error {
		c.add(resp.GetProgress(), resp.GetMetadata(), resp.GetFile(), opts.OnProgress)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return c.result(procedure)
}

// GenerateTalking lip-syncs portrait to audio. The portrait is a medium
// shot facing the camera with the mouth closed (PNG, JPEG or WebP); a
// landscape portrait gives an 832x480 video, a portrait orientation 480x832.
// The audio (WAV, MP3, FLAC, M4A or OGG) is the voice track and ends up in
// the video unchanged. The prompt describes the speaker and the scene.
func (s *Service) GenerateTalking(
	ctx context.Context,
	prompt string,
	portrait, audio rootpullersdk.Upload,
	opts *TalkingOptions,
) (*Result, error) {
	if opts == nil {
		opts = &TalkingOptions{}
	}

	ctx = transport.EnsureComfyUIDeployment(ctx, s.comfyUIDeployment)
	procedure := videoconnect.VideoGeneratorServiceGenerateTalkingVideoProcedure
	stream := s.rpc.GenerateTalkingVideo(ctx)

	frames := streamio.Frames(
		&videopb.GenerateTalkingVideoRequest{Request: &videopb.GenerateTalkingVideoRequest_Params_{Params: opts.toProto(prompt)}},
		streamio.FileChunkFrames(portrait, func(c *commonpb.FileChunk) *videopb.GenerateTalkingVideoRequest {
			return &videopb.GenerateTalkingVideoRequest{Request: &videopb.GenerateTalkingVideoRequest_Portrait{Portrait: c}}
		}),
		streamio.FileChunkFrames(audio, func(c *commonpb.FileChunk) *videopb.GenerateTalkingVideoRequest {
			return &videopb.GenerateTalkingVideoRequest{Request: &videopb.GenerateTalkingVideoRequest_Audio{Audio: c}}
		}),
	)

	var c collector

	err := streamio.UploadCollect(stream, procedure, frames, func(resp *videopb.GenerateTalkingVideoResponse) error {
		c.add(resp.GetProgress(), resp.GetMetadata(), resp.GetFile(), opts.OnProgress)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return c.result(procedure)
}

// collector gathers one call's response frames; both RPCs share their shape.
type collector struct {
	files    streamio.FileCollector
	meta     Metadata
	gotMeta  bool
	gotFiles bool
}

func (c *collector) add(
	progress *videopb.GenerationProgress,
	meta *videopb.VideoMetadata,
	file *commonpb.FileChunk,
	onProgress func(Progress),
) {
	switch {
	case progress != nil:
		if onProgress != nil {
			onProgress(progressFromProto(progress))
		}
	case meta != nil:
		c.meta = metadataFromProto(meta)
		c.gotMeta = true
	case file != nil:
		c.files.Add(file)
		c.gotFiles = true
	}
}

func (c *collector) result(procedure string) (*Result, error) {
	files, err := c.files.Files()
	if err != nil {
		return nil, err
	}

	if !c.gotMeta || !c.gotFiles || len(files) != 1 {
		return nil, apierr.New(connect.CodeInternal, procedure+": the server sent no complete video", "", 0, nil)
	}

	return &Result{Video: files[0], Metadata: c.meta}, nil
}
