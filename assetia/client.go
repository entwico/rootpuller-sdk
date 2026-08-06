// Package assetia wraps
// com.entwico.rootpuller.assetia.MediaProcessingService: deterministic
// media processing — image crop/rotate/resize/watermark/re-encode, video
// transcoding (H.265, AV1), preview extraction, and metadata probing. It
// complements the AI-model image services (unshakaler, facefixer,
// bgremover, painter), which transform content; this service transforms
// representation.
package assetia

import (
	"context"
	"iter"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	assetiapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/assetia"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/assetia/assetiaconnect"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the MediaProcessingService.
type Service struct {
	rpc assetiaconnect.MediaProcessingServiceClient
}

// Option configures a Service at construction.
type Option func(*Service)

// NewService builds a MediaProcessingService client on the sdk
// connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: assetiaconnect.NewMediaProcessingServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// TransformImage calls MediaProcessingService/TransformImage: it sends
// the params frame first, then the source image chunks, then — when
// opts.Watermark is non-nil — the watermark image chunks, strictly in
// that order, and half-closes. The server replies with one metadata
// frame followed by the transformed image chunks.
func (s *Service) TransformImage(ctx context.Context, image rootpullersdk.Upload, opts *TransformImageOptions) (*TransformImageResult, error) {
	if opts == nil {
		opts = &TransformImageOptions{}
	}

	pb, err := opts.toProto()
	if err != nil {
		return nil, err
	}

	procedure := assetiaconnect.MediaProcessingServiceTransformImageProcedure
	stream := s.rpc.TransformImage(ctx)

	tails := []iter.Seq2[*assetiapb.TransformImageRequest, error]{
		streamio.FileChunkFrames(image, func(chunk *commonpb.FileChunk) *assetiapb.TransformImageRequest {
			return &assetiapb.TransformImageRequest{Request: &assetiapb.TransformImageRequest_File{File: chunk}}
		}),
	}
	if opts.Watermark != nil {
		tails = append(tails, streamio.FileChunkFrames(opts.Watermark.Image, func(chunk *commonpb.FileChunk) *assetiapb.TransformImageRequest {
			return &assetiapb.TransformImageRequest{Request: &assetiapb.TransformImageRequest_WatermarkFile{WatermarkFile: chunk}}
		}))
	}

	frames := streamio.Frames(
		&assetiapb.TransformImageRequest{Request: &assetiapb.TransformImageRequest_Params_{Params: pb}},
		tails...,
	)

	var (
		collector streamio.FileCollector
		res       TransformImageResult
	)

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *assetiapb.TransformImageResponse) error {
		switch r := resp.GetResponse().(type) {
		case *assetiapb.TransformImageResponse_Metadata:
			res.Original = fromProtoImageMetadata(r.Metadata.GetOriginal())
			res.Result = fromProtoImageMetadata(r.Metadata.GetResult())
			res.ProcessingTime = protoconv.MsToDuration(r.Metadata.GetProcessingTimeMs())
		case *assetiapb.TransformImageResponse_File:
			collector.Add(r.File)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	files, err := collector.Files()
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, apierr.New(connect.CodeInternal, "server returned no image", procedure, 0, nil)
	}

	res.File = files[0]

	return &res, nil
}

// TransformVideo calls MediaProcessingService/TransformVideo: it sends
// the params frame first, then the source video chunks, in that order,
// and half-closes. The server streams progress frames while transcoding
// (forwarded to opts.OnProgress when set), then one metadata frame
// followed by the transcoded video chunks.
func (s *Service) TransformVideo(ctx context.Context, video rootpullersdk.Upload, opts *TransformVideoOptions) (*TransformVideoResult, error) {
	if opts == nil {
		opts = &TransformVideoOptions{}
	}

	pb, err := opts.toProto()
	if err != nil {
		return nil, err
	}

	procedure := assetiaconnect.MediaProcessingServiceTransformVideoProcedure
	stream := s.rpc.TransformVideo(ctx)

	frames := streamio.Frames(
		&assetiapb.TransformVideoRequest{Request: &assetiapb.TransformVideoRequest_Params_{Params: pb}},
		streamio.FileChunkFrames(video, func(chunk *commonpb.FileChunk) *assetiapb.TransformVideoRequest {
			return &assetiapb.TransformVideoRequest{Request: &assetiapb.TransformVideoRequest_File{File: chunk}}
		}),
	)

	var (
		collector streamio.FileCollector
		res       TransformVideoResult
	)

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *assetiapb.TransformVideoResponse) error {
		switch r := resp.GetResponse().(type) {
		case *assetiapb.TransformVideoResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(r.Progress))
			}
		case *assetiapb.TransformVideoResponse_Metadata:
			res.Original = fromProtoVideoMetadata(r.Metadata.GetOriginal())
			res.Result = fromProtoVideoMetadata(r.Metadata.GetResult())
			res.ProcessingTime = protoconv.MsToDuration(r.Metadata.GetProcessingTimeMs())
		case *assetiapb.TransformVideoResponse_File:
			collector.Add(r.File)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	files, err := collector.Files()
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, apierr.New(connect.CodeInternal, "server returned no video", procedure, 0, nil)
	}

	res.File = files[0]

	return &res, nil
}

// GeneratePreview calls MediaProcessingService/GeneratePreview: it sends
// the params frame first, then the source media (image or video) chunks,
// in that order, and half-closes. The server replies with one metadata
// frame followed by the preview image chunks.
func (s *Service) GeneratePreview(ctx context.Context, media rootpullersdk.Upload, opts *PreviewOptions) (*PreviewResult, error) {
	if opts == nil {
		opts = &PreviewOptions{}
	}

	pb, err := opts.toProto()
	if err != nil {
		return nil, err
	}

	procedure := assetiaconnect.MediaProcessingServiceGeneratePreviewProcedure
	stream := s.rpc.GeneratePreview(ctx)

	frames := streamio.Frames(
		&assetiapb.GeneratePreviewRequest{Request: &assetiapb.GeneratePreviewRequest_Params_{Params: pb}},
		streamio.FileChunkFrames(media, func(chunk *commonpb.FileChunk) *assetiapb.GeneratePreviewRequest {
			return &assetiapb.GeneratePreviewRequest{Request: &assetiapb.GeneratePreviewRequest_File{File: chunk}}
		}),
	)

	var (
		collector streamio.FileCollector
		res       PreviewResult
	)

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *assetiapb.GeneratePreviewResponse) error {
		switch r := resp.GetResponse().(type) {
		case *assetiapb.GeneratePreviewResponse_Metadata:
			res.Source = fromProtoMediaMetadata(r.Metadata.GetSource())
			res.Result = fromProtoImageMetadata(r.Metadata.GetResult())
			res.ProcessingTime = protoconv.MsToDuration(r.Metadata.GetProcessingTimeMs())
		case *assetiapb.GeneratePreviewResponse_File:
			collector.Add(r.File)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	files, err := collector.Files()
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, apierr.New(connect.CodeInternal, "server returned no preview", procedure, 0, nil)
	}

	res.File = files[0]

	return &res, nil
}

// ProbeMedia calls MediaProcessingService/ProbeMedia (a client stream):
// it streams the media chunks, half-closes, and returns the decoded
// metadata — exactly one of Image and Video is non-nil.
func (s *Service) ProbeMedia(ctx context.Context, media rootpullersdk.Upload) (*MediaMetadata, error) {
	procedure := assetiaconnect.MediaProcessingServiceProbeMediaProcedure
	stream := s.rpc.ProbeMedia(ctx)

	frames := streamio.FileChunkFrames(media, func(chunk *commonpb.FileChunk) *assetiapb.ProbeMediaRequest {
		return &assetiapb.ProbeMediaRequest{File: chunk}
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

	meta := fromProtoMediaMetadata(resp.Msg.GetMetadata())

	return &meta, nil
}

func closeAndWrap(stream *connect.ClientStreamForClient[assetiapb.ProbeMediaRequest, assetiapb.ProbeMediaResponse], err error, procedure string) error {
	// After a Send failure the definitive error comes from
	// CloseAndReceive.
	if _, cerr := stream.CloseAndReceive(); cerr != nil {
		return transport.WrapError(cerr, procedure)
	}

	return transport.WrapError(err, procedure)
}
