// Package bgremover wraps
// com.entwico.rootpuller.bgremover.BackgroundRemoverService: AI
// background removal.
package bgremover

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/common"
	bgremoverpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/bgremover"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/bgremover/bgremoverconnect"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client calls the BackgroundRemoverService. Obtain one from
// rootpuller.Client.
type Client struct {
	rpc bgremoverconnect.BackgroundRemoverServiceClient
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: bgremoverconnect.NewBackgroundRemoverServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// MorphologyMode selects the order of morphological operations when both
// Erode and Dilate are set. The zero value leaves the choice to the
// server.
type MorphologyMode string

const (
	// MorphologyModeUnspecified keeps the server default.
	MorphologyModeUnspecified MorphologyMode = ""
	// MorphologyModeOpening erodes then dilates: removes noise and
	// artifacts from mask edges.
	MorphologyModeOpening MorphologyMode = "opening"
	// MorphologyModeClosing dilates then erodes: fills holes and gaps in
	// the mask.
	MorphologyModeClosing MorphologyMode = "closing"
)

var morphologyModeToProto = map[MorphologyMode]bgremoverpb.MorphologyMode{
	MorphologyModeUnspecified: bgremoverpb.MorphologyMode_MORPHOLOGY_MODE_UNSPECIFIED,
	MorphologyModeOpening:     bgremoverpb.MorphologyMode_MORPHOLOGY_MODE_OPENING,
	MorphologyModeClosing:     bgremoverpb.MorphologyMode_MORPHOLOGY_MODE_CLOSING,
}

// Params configures RemoveBackground. Nil pointer fields keep the server
// defaults.
type Params struct {
	// Threshold is the mask threshold, 0.0-1.0 (default 0.5); lower keeps
	// more edges.
	Threshold *float32
	// Feather is the feathering radius for softer edges, 0-20 pixels
	// (default 0).
	Feather *float32
	// Erode shrinks the foreground by N pixels, 0-20 (default 0).
	Erode *int
	// Dilate expands the foreground by N pixels, 0-20 (default 0).
	Dilate *int
	// MorphologyMode orders the operations when both Erode and Dilate are
	// set (default opening).
	MorphologyMode MorphologyMode
}

func (p *Params) toProto() (*bgremoverpb.RemoveBackgroundRequest_Params, error) {
	mode, ok := morphologyModeToProto[p.MorphologyMode]
	if !ok {
		return nil, apierror.New(connect.CodeInvalidArgument, fmt.Sprintf("unknown morphology mode %q", p.MorphologyMode), "", 0, nil)
	}

	msg := &bgremoverpb.RemoveBackgroundRequest_Params{
		Threshold: p.Threshold,
		Feather:   p.Feather,
		Erode:     protoconv.Int32Ptr(p.Erode),
		Dilate:    protoconv.Int32Ptr(p.Dilate),
	}
	if mode != bgremoverpb.MorphologyMode_MORPHOLOGY_MODE_UNSPECIFIED {
		msg.MorphologyMode = &mode
	}

	return msg, nil
}

// Metadata describes the background removal result.
type Metadata struct {
	// ProcessingTime is the server-side processing duration.
	ProcessingTime time.Duration
	// Size is the output image size.
	Size common.ImageSize
	// Device is the compute device used: cuda, mps, or cpu.
	Device string
	// MaskConfidence is the average mask probability before thresholding
	// (0-1).
	MaskConfidence float32
	// ForegroundPercent is the percentage of the image detected as
	// foreground.
	ForegroundPercent float32
	// BoundingBox is the foreground bounding box, nil when no foreground
	// was detected.
	BoundingBox *common.BoundingBox
}

func fromProtoMetadata(pb *bgremoverpb.RemoveBackgroundMetadata) Metadata {
	m := Metadata{
		ProcessingTime:    time.Duration(pb.GetProcessingTimeMs()) * time.Millisecond,
		Size:              protoconv.FromProtoImageSize(pb.GetSize()),
		Device:            pb.GetDevice(),
		MaskConfidence:    pb.GetMaskConfidence(),
		ForegroundPercent: pb.GetForegroundPercent(),
	}
	if pb.GetBoundingBox() != nil {
		box := protoconv.FromProtoBoundingBox(pb.GetBoundingBox())
		m.BoundingBox = &box
	}

	return m
}

// Result is the outcome of RemoveBackground: the cut-out image and the
// metadata the server reported for it.
type Result struct {
	File     common.File
	Metadata Metadata
}

// RemoveBackground calls BackgroundRemoverService/RemoveBackground:
// streams the image up, then returns the image with its background
// removed alongside the removal metadata.
func (c *Client) RemoveBackground(ctx context.Context, params *Params, image common.Upload) (*Result, error) {
	pb, err := params.toProto()
	if err != nil {
		return nil, err
	}

	procedure := bgremoverconnect.BackgroundRemoverServiceRemoveBackgroundProcedure
	stream := c.rpc.RemoveBackground(ctx)

	frames := streamio.Frames(
		&bgremoverpb.RemoveBackgroundRequest{Request: &bgremoverpb.RemoveBackgroundRequest_Params_{Params: pb}},
		streamio.FileChunkFrames(image, func(chunk *commonpb.FileChunk) *bgremoverpb.RemoveBackgroundRequest {
			return &bgremoverpb.RemoveBackgroundRequest{Request: &bgremoverpb.RemoveBackgroundRequest_File{File: chunk}}
		}),
	)

	var (
		meta      Metadata
		collector streamio.FileCollector
	)

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *bgremoverpb.RemoveBackgroundResponse) error {
		switch r := resp.GetResponse().(type) {
		case *bgremoverpb.RemoveBackgroundResponse_Metadata:
			meta = fromProtoMetadata(r.Metadata)
		case *bgremoverpb.RemoveBackgroundResponse_File:
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

	return &Result{File: files[0], Metadata: meta}, nil
}
