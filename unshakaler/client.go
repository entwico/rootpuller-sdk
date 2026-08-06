// Package unshakaler wraps
// com.entwico.rootpuller.unshakaler.UnshakalerService: AI image
// upscaling.
package unshakaler

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	unshakalerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/unshakaler"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/unshakaler/unshakalerconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
)

// Service calls the UnshakalerService.
type Service struct {
	rpc          unshakalerconnect.UnshakalerServiceClient
	defaultModel string
}

// Option configures a Service at construction.
type Option func(*Service)

// WithDefaultModel sets the upscaling model used when a call's Options
// leave Model empty.
func WithDefaultModel(model string) Option {
	return func(s *Service) { s.defaultModel = model }
}

// NewService builds an UnshakalerService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: unshakalerconnect.NewUnshakalerServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Options tunes an UpscaleImage call. Nil keeps all defaults.
type Options struct {
	// Model overrides the service default upscaling model (e.g.
	// "realesrgan-x4plus").
	Model string
	// ScaleFactor is the upscale multiplier: 2, 3, or 4 (0 = server
	// default).
	ScaleFactor int
	// Format selects the output encoding.
	Format rootpullersdk.ImageFormat
	// Compress is the output compression level, 1-100 (0 = server
	// default).
	Compress int
	// Resize post-resizes the output, e.g. "800x600" (empty = no resize).
	Resize string
	// Width post-resizes the output to a width, keeping aspect ratio
	// (empty = no resize).
	Width string
	// TileSize tunes GPU tiling, e.g. "0,0,0" for multi-GPU (empty =
	// server default).
	TileSize string
	// EnableTTA enables test-time augmentation (slower, higher quality).
	EnableTTA bool
	// OnProgress, when set, receives progress percentages (0-100) as the
	// server reports them. Called synchronously from the receive loop.
	OnProgress func(percent float32)
}

func (o *Options) toProto(model string) (*unshakalerpb.UpscaleImageRequest_Params, error) {
	format, ok := protoconv.ToProtoImageFormat(o.Format)
	if !ok {
		return nil, apierr.New(connect.CodeInvalidArgument, fmt.Sprintf("unknown image format %q", o.Format), "", 0, nil)
	}

	msg := &unshakalerpb.UpscaleImageRequest_Params{ModelName: model}
	if o.ScaleFactor > 0 {
		msg.ScaleFactor = protoconv.Int32Ptr(&o.ScaleFactor)
	}

	if format != commonpb.ImageFormat_IMAGE_FORMAT_UNSPECIFIED {
		msg.Format = &format
	}

	if o.Compress > 0 {
		msg.Compress = protoconv.Int32Ptr(&o.Compress)
	}

	if o.Resize != "" {
		msg.Resize = &o.Resize
	}

	if o.Width != "" {
		msg.Width = &o.Width
	}

	if o.TileSize != "" {
		msg.TileSize = &o.TileSize
	}

	if o.EnableTTA {
		msg.EnableTta = &o.EnableTTA
	}

	return msg, nil
}

// UpscaleImage calls UnshakalerService/UpscaleImage: it sends the params
// frame first, then the image chunks, in that order, and half-closes.
// Progress frames are forwarded to opts.OnProgress when set; the
// reassembled upscaled image is the result.
func (s *Service) UpscaleImage(ctx context.Context, image rootpullersdk.Upload, opts *Options) (*rootpullersdk.File, error) {
	if opts == nil {
		opts = &Options{}
	}

	model := opts.Model
	if model == "" {
		model = s.defaultModel
	}

	pb, err := opts.toProto(model)
	if err != nil {
		return nil, err
	}

	procedure := unshakalerconnect.UnshakalerServiceUpscaleImageProcedure
	stream := s.rpc.UpscaleImage(ctx)

	frames := streamio.Frames(
		&unshakalerpb.UpscaleImageRequest{Request: &unshakalerpb.UpscaleImageRequest_Params_{Params: pb}},
		streamio.FileChunkFrames(image, func(chunk *commonpb.FileChunk) *unshakalerpb.UpscaleImageRequest {
			return &unshakalerpb.UpscaleImageRequest{Request: &unshakalerpb.UpscaleImageRequest_File{File: chunk}}
		}),
	)

	var collector streamio.FileCollector

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *unshakalerpb.UpscaleImageResponse) error {
		switch r := resp.GetResponse().(type) {
		case *unshakalerpb.UpscaleImageResponse_ProgressPercentage:
			if opts.OnProgress != nil {
				opts.OnProgress(r.ProgressPercentage)
			}
		case *unshakalerpb.UpscaleImageResponse_File:
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

	return &files[0], nil
}
