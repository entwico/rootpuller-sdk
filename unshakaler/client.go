// Package unshakaler wraps
// com.entwico.rootpuller.unshakaler.UnshakalerService: AI image
// upscaling.
package unshakaler

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/common"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	unshakalerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/unshakaler"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/unshakaler/unshakalerconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client calls the UnshakalerService. Obtain one from
// rootpuller.Client.Unshakaler.
type Client struct {
	rpc unshakalerconnect.UnshakalerServiceClient
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: unshakalerconnect.NewUnshakalerServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// UpscaleParams configures UpscaleImage. Nil pointer fields keep the
// server defaults.
type UpscaleParams struct {
	// Model is the upscaling model name (e.g. "realesrgan-x4plus").
	Model string
	// ScaleFactor is the upscale multiplier (2, 3, or 4).
	ScaleFactor *int
	// Format selects the output encoding.
	Format common.ImageFormat
	// Compress is the output compression level.
	Compress *int
	// Resize post-resizes the output, e.g. "800x600".
	Resize *string
	// Width post-resizes the output to a width, keeping aspect ratio.
	Width *string
	// TileSize tunes GPU tiling, e.g. "0,0,0" for multi-GPU.
	TileSize *string
	// EnableTTA enables test-time augmentation (slower, higher quality).
	EnableTTA *bool
	// OnProgress, when set, receives progress percentages (0-100) as the
	// server reports them. Called synchronously from the receive loop.
	OnProgress func(percent float32)
}

func (p *UpscaleParams) toProto() (*unshakalerpb.UpscaleImageRequest_Params, error) {
	format, ok := protoconv.ToProtoImageFormat(p.Format)
	if !ok {
		return nil, apierror.New(connect.CodeInvalidArgument, fmt.Sprintf("unknown image format %q", p.Format), "", 0, nil)
	}

	msg := &unshakalerpb.UpscaleImageRequest_Params{
		ModelName:   p.Model,
		ScaleFactor: protoconv.Int32Ptr(p.ScaleFactor),
		Compress:    protoconv.Int32Ptr(p.Compress),
		Resize:      p.Resize,
		Width:       p.Width,
		TileSize:    p.TileSize,
		EnableTta:   p.EnableTTA,
	}
	if format != commonpb.ImageFormat_IMAGE_FORMAT_UNSPECIFIED {
		msg.Format = &format
	}

	return msg, nil
}

// UpscaleImage calls UnshakalerService/UpscaleImage: streams the image
// up, then returns the upscaled result. Progress frames are forwarded to
// params.OnProgress when set.
func (c *Client) UpscaleImage(ctx context.Context, params *UpscaleParams, image common.Upload) (*common.File, error) {
	pb, err := params.toProto()
	if err != nil {
		return nil, err
	}

	procedure := unshakalerconnect.UnshakalerServiceUpscaleImageProcedure
	stream := c.rpc.UpscaleImage(ctx)

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
			if params.OnProgress != nil {
				params.OnProgress(r.ProgressPercentage)
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
		return nil, apierror.New(connect.CodeInternal, "server returned no image", procedure, 0, nil)
	}

	return &files[0], nil
}
