// Package bgremover wraps
// com.entwico.rootpuller.bgremover.BackgroundRemoverService: AI
// background removal.
package bgremover

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	bgremoverpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/bgremover"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/bgremover/bgremoverconnect"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
)

// Service calls the BackgroundRemoverService.
type Service struct {
	rpc bgremoverconnect.BackgroundRemoverServiceClient
}

// Option configures a Service at construction.
type Option func(*Service)

// NewService builds a BackgroundRemoverService client on the sdk
// connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: bgremoverconnect.NewBackgroundRemoverServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
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

// Options tunes a RemoveBackground call. Nil keeps all defaults.
type Options struct {
	// Threshold is the mask threshold, 0.0-1.0 (default 0.5); lower keeps
	// more edges. Nil keeps the server default.
	Threshold *float32
	// Feather is the feathering radius for softer edges, 0-20 pixels
	// (default 0). Nil keeps the server default.
	Feather *float32
	// Erode shrinks the foreground by N pixels, 1-20 (0 = server
	// default).
	Erode int
	// Dilate expands the foreground by N pixels, 1-20 (0 = server
	// default).
	Dilate int
	// MorphologyMode orders the operations when both Erode and Dilate are
	// set (default opening).
	MorphologyMode MorphologyMode
}

func (o *Options) toProto() (*bgremoverpb.RemoveBackgroundRequest_Params, error) {
	mode, ok := morphologyModeToProto[o.MorphologyMode]
	if !ok {
		return nil, apierr.New(connect.CodeInvalidArgument, fmt.Sprintf("unknown morphology mode %q", o.MorphologyMode), "", 0, nil)
	}

	msg := &bgremoverpb.RemoveBackgroundRequest_Params{
		Threshold: o.Threshold,
		Feather:   o.Feather,
	}
	if o.Erode > 0 {
		msg.Erode = protoconv.Int32Ptr(&o.Erode)
	}

	if o.Dilate > 0 {
		msg.Dilate = protoconv.Int32Ptr(&o.Dilate)
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
	Size rootpullersdk.ImageSize
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
	BoundingBox *rootpullersdk.BoundingBox
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
	File     rootpullersdk.File
	Metadata Metadata
}

// RemoveBackground calls BackgroundRemoverService/RemoveBackground: it
// sends the params frame first, then the image chunks, in that order, and
// half-closes. It returns the image with its background removed alongside
// the removal metadata.
func (s *Service) RemoveBackground(ctx context.Context, image rootpullersdk.Upload, opts *Options) (*Result, error) {
	if opts == nil {
		opts = &Options{}
	}

	pb, err := opts.toProto()
	if err != nil {
		return nil, err
	}

	procedure := bgremoverconnect.BackgroundRemoverServiceRemoveBackgroundProcedure
	stream := s.rpc.RemoveBackground(ctx)

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
		return nil, apierr.New(connect.CodeInternal, "server returned no image", procedure, 0, nil)
	}

	return &Result{File: files[0], Metadata: meta}, nil
}
