package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/assetia"
	assetiapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/assetia"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/assetia/assetiaconnect"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// AssetiaImageMetadata is the TransformImage metadata-frame fixture.
type AssetiaImageMetadata struct {
	Original       assetia.ImageMetadata
	Result         assetia.ImageMetadata
	ProcessingTime time.Duration
}

// AssetiaVideoMetadata is the TransformVideo metadata-frame fixture.
type AssetiaVideoMetadata struct {
	Original       assetia.VideoMetadata
	Result         assetia.VideoMetadata
	ProcessingTime time.Duration
}

// AssetiaPreviewMetadata is the GeneratePreview metadata-frame fixture.
type AssetiaPreviewMetadata struct {
	Source         assetia.MediaMetadata
	Result         assetia.ImageMetadata
	ProcessingTime time.Duration
}

// Assetia is a facade-typed fake MediaProcessingService. It enforces the
// wire protocol strictly (params frame first and exactly once, chunks
// ≤2 MiB, no work before the client half-closes) so SDK regressions
// surface as test failures. Nil hooks echo the input with an
// RPC-specific name prefix ("transformed-", "transcoded-", "preview-");
// a nil ProbeFunc answers with canned image metadata. The bidi RPCs
// stream their metadata frame (built from the fixture, or synthesized
// minimally when nil) ahead of the file chunks, like the real server;
// TransformVideo streams Progress frames before the metadata.
type Assetia struct {
	TransformImageFunc func(image rootpullersdk.File, watermark *rootpullersdk.File) (*rootpullersdk.File, error)
	TransformVideoFunc func(video rootpullersdk.File) (*rootpullersdk.File, error)
	PreviewFunc        func(media rootpullersdk.File) (*rootpullersdk.File, error)
	ProbeFunc          func(media rootpullersdk.File) (*assetia.MediaMetadata, error)

	ImageMetadata   *AssetiaImageMetadata
	VideoMetadata   *AssetiaVideoMetadata
	PreviewMetadata *AssetiaPreviewMetadata
	Progress        []assetia.Progress
}

func (f *Assetia) register(mux *http.ServeMux) {
	mux.Handle(assetiaconnect.NewMediaProcessingServiceHandler(&assetiaHandler{fake: f}))
}

type assetiaHandler struct {
	fake *Assetia
}

func (h *assetiaHandler) TransformImage(_ context.Context, stream *connect.BidiStream[assetiapb.TransformImageRequest, assetiapb.TransformImageResponse]) error {
	var (
		params           *assetiapb.TransformImageRequest_Params
		input, watermark rootpullersdk.File
	)

	watermarkSeen := false
	// Like the real server: drain the full request before any work.
	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		switch r := req.GetRequest().(type) {
		case *assetiapb.TransformImageRequest_Params_:
			if params != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *assetiapb.TransformImageRequest_File:
			if params == nil {
				return errBeforeParams("file")
			}

			if err := appendChunk(&input, r.File); err != nil {
				return err
			}
		case *assetiapb.TransformImageRequest_WatermarkFile:
			if params == nil {
				return errBeforeParams("watermark_file")
			}

			if err := appendChunk(&watermark, r.WatermarkFile); err != nil {
				return err
			}

			watermarkSeen = true
		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if params == nil {
		return invalidArgument(errMissingParams)
	}

	transform := h.fake.TransformImageFunc
	if transform == nil {
		transform = func(image rootpullersdk.File, _ *rootpullersdk.File) (*rootpullersdk.File, error) {
			return &rootpullersdk.File{Name: "transformed-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		}
	}

	var wm *rootpullersdk.File
	if watermarkSeen {
		wm = &watermark
	}

	result, err := transform(input, wm)
	if err != nil {
		return err
	}

	meta := h.fake.ImageMetadata
	if meta == nil {
		meta = &AssetiaImageMetadata{}
	}

	metaPb, err := toProtoAssetiaImageResultMetadata(meta)
	if err != nil {
		return err
	}

	if err := stream.Send(&assetiapb.TransformImageResponse{
		Response: &assetiapb.TransformImageResponse_Metadata{Metadata: metaPb},
	}); err != nil {
		return err
	}

	return SendFileChunks(result, func(chunk *commonpb.FileChunk) error {
		return stream.Send(&assetiapb.TransformImageResponse{
			Response: &assetiapb.TransformImageResponse_File{File: chunk},
		})
	})
}

func (h *assetiaHandler) TransformVideo(_ context.Context, stream *connect.BidiStream[assetiapb.TransformVideoRequest, assetiapb.TransformVideoResponse]) error {
	var (
		params *assetiapb.TransformVideoRequest_Params
		input  rootpullersdk.File
	)
	// Like the real server: drain the full request before any work.

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		switch r := req.GetRequest().(type) {
		case *assetiapb.TransformVideoRequest_Params_:
			if params != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *assetiapb.TransformVideoRequest_File:
			if params == nil {
				return errBeforeParams("file")
			}

			if err := appendChunk(&input, r.File); err != nil {
				return err
			}
		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if params == nil {
		return invalidArgument(errMissingParams)
	}

	transform := h.fake.TransformVideoFunc
	if transform == nil {
		transform = func(video rootpullersdk.File) (*rootpullersdk.File, error) {
			return &rootpullersdk.File{Name: "transcoded-" + video.Name, MIMEType: video.MIMEType, Data: video.Data}, nil
		}
	}

	result, err := transform(input)
	if err != nil {
		return err
	}

	for _, p := range h.fake.Progress {
		pb := &assetiapb.TranscodeProgress{Percentage: p.Percentage}
		if p.FPS != 0 {
			fps := p.FPS
			pb.Fps = &fps
		}

		if err := stream.Send(&assetiapb.TransformVideoResponse{
			Response: &assetiapb.TransformVideoResponse_Progress{Progress: pb},
		}); err != nil {
			return err
		}
	}

	meta := h.fake.VideoMetadata
	if meta == nil {
		meta = &AssetiaVideoMetadata{}
	}

	metaPb, err := toProtoAssetiaVideoResultMetadata(meta)
	if err != nil {
		return err
	}

	if err := stream.Send(&assetiapb.TransformVideoResponse{
		Response: &assetiapb.TransformVideoResponse_Metadata{Metadata: metaPb},
	}); err != nil {
		return err
	}

	return SendFileChunks(result, func(chunk *commonpb.FileChunk) error {
		return stream.Send(&assetiapb.TransformVideoResponse{
			Response: &assetiapb.TransformVideoResponse_File{File: chunk},
		})
	})
}

func (h *assetiaHandler) GeneratePreview(_ context.Context, stream *connect.BidiStream[assetiapb.GeneratePreviewRequest, assetiapb.GeneratePreviewResponse]) error {
	var (
		params *assetiapb.GeneratePreviewRequest_Params
		input  rootpullersdk.File
	)
	// Like the real server: drain the full request before any work.

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		switch r := req.GetRequest().(type) {
		case *assetiapb.GeneratePreviewRequest_Params_:
			if params != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *assetiapb.GeneratePreviewRequest_File:
			if params == nil {
				return errBeforeParams("file")
			}

			if err := appendChunk(&input, r.File); err != nil {
				return err
			}
		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if params == nil {
		return invalidArgument(errMissingParams)
	}

	preview := h.fake.PreviewFunc
	if preview == nil {
		preview = func(media rootpullersdk.File) (*rootpullersdk.File, error) {
			return &rootpullersdk.File{Name: "preview-" + media.Name, MIMEType: "image/webp", Data: media.Data}, nil
		}
	}

	result, err := preview(input)
	if err != nil {
		return err
	}

	meta := h.fake.PreviewMetadata
	if meta == nil {
		meta = &AssetiaPreviewMetadata{}
	}

	metaPb, err := toProtoAssetiaPreviewMetadata(meta)
	if err != nil {
		return err
	}

	if err := stream.Send(&assetiapb.GeneratePreviewResponse{
		Response: &assetiapb.GeneratePreviewResponse_Metadata{Metadata: metaPb},
	}); err != nil {
		return err
	}

	return SendFileChunks(result, func(chunk *commonpb.FileChunk) error {
		return stream.Send(&assetiapb.GeneratePreviewResponse{
			Response: &assetiapb.GeneratePreviewResponse_File{File: chunk},
		})
	})
}

func (h *assetiaHandler) ProbeMedia(_ context.Context, stream *connect.ClientStream[assetiapb.ProbeMediaRequest]) (*connect.Response[assetiapb.ProbeMediaResponse], error) {
	var input rootpullersdk.File
	// Like the real server: drain the full request before any work.
	for stream.Receive() {
		if err := appendChunk(&input, stream.Msg().GetFile()); err != nil {
			return nil, err
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	probe := h.fake.ProbeFunc
	if probe == nil {
		probe = func(rootpullersdk.File) (*assetia.MediaMetadata, error) {
			return &assetia.MediaMetadata{Image: &assetia.ImageMetadata{
				Size:       rootpullersdk.ImageSize{Width: 1, Height: 1},
				Format:     rootpullersdk.ImageFormatPNG,
				ColorSpace: "srgb",
				Channels:   3,
			}}, nil
		}
	}

	meta, err := probe(input)
	if err != nil {
		return nil, err
	}

	pb, err := toProtoAssetiaMediaMetadata(meta)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&assetiapb.ProbeMediaResponse{Metadata: pb}), nil
}

// assetiaExtraToProto maps a facade extras map to the optional
// google.protobuf.Struct; a nil map stays a nil proto struct.
func assetiaExtraToProto(m map[string]any) (*structpb.Struct, error) {
	if m == nil {
		return nil, nil
	}

	return structpb.NewStruct(m)
}

func toProtoAssetiaImageMetadata(m *assetia.ImageMetadata) (*assetiapb.ImageMetadata, error) {
	extra, err := assetiaExtraToProto(m.Extra)
	if err != nil {
		return nil, err
	}

	format, _ := protoconv.ToProtoImageFormat(m.Format)

	return &assetiapb.ImageMetadata{
		Size:            toProtoImageSize(m.Size),
		Format:          format,
		ColorSpace:      m.ColorSpace,
		HasAlpha:        m.HasAlpha,
		HasIccProfile:   m.HasICCProfile,
		Channels:        int32(m.Channels),        //nolint:gosec // test-fixture counts fit int32
		ExifOrientation: int32(m.EXIFOrientation), //nolint:gosec // test-fixture tags fit int32
		Extra:           extra,
	}, nil
}

func toProtoAssetiaVideoMetadata(m *assetia.VideoMetadata) (*assetiapb.VideoMetadata, error) {
	extra, err := assetiaExtraToProto(m.Extra)
	if err != nil {
		return nil, err
	}

	pb := &assetiapb.VideoMetadata{
		Size:       toProtoImageSize(m.Size),
		Codec:      m.Codec,
		DurationMs: m.Duration.Milliseconds(),
		FrameRate:  m.FrameRate,
		Extra:      extra,
	}
	if m.AudioCodec != "" {
		codec := m.AudioCodec
		pb.AudioCodec = &codec
	}

	return pb, nil
}

func toProtoAssetiaMediaMetadata(m *assetia.MediaMetadata) (*assetiapb.MediaMetadata, error) {
	switch {
	case m.Image != nil:
		img, err := toProtoAssetiaImageMetadata(m.Image)
		if err != nil {
			return nil, err
		}

		return &assetiapb.MediaMetadata{Metadata: &assetiapb.MediaMetadata_Image{Image: img}}, nil
	case m.Video != nil:
		vid, err := toProtoAssetiaVideoMetadata(m.Video)
		if err != nil {
			return nil, err
		}

		return &assetiapb.MediaMetadata{Metadata: &assetiapb.MediaMetadata_Video{Video: vid}}, nil
	default:
		return &assetiapb.MediaMetadata{}, nil
	}
}

func toProtoAssetiaImageResultMetadata(m *AssetiaImageMetadata) (*assetiapb.TransformImageMetadata, error) {
	original, err := toProtoAssetiaImageMetadata(&m.Original)
	if err != nil {
		return nil, err
	}

	result, err := toProtoAssetiaImageMetadata(&m.Result)
	if err != nil {
		return nil, err
	}

	return &assetiapb.TransformImageMetadata{
		ProcessingTimeMs: m.ProcessingTime.Milliseconds(),
		Original:         original,
		Result:           result,
	}, nil
}

func toProtoAssetiaVideoResultMetadata(m *AssetiaVideoMetadata) (*assetiapb.TransformVideoMetadata, error) {
	original, err := toProtoAssetiaVideoMetadata(&m.Original)
	if err != nil {
		return nil, err
	}

	result, err := toProtoAssetiaVideoMetadata(&m.Result)
	if err != nil {
		return nil, err
	}

	return &assetiapb.TransformVideoMetadata{
		ProcessingTimeMs: m.ProcessingTime.Milliseconds(),
		Original:         original,
		Result:           result,
	}, nil
}

func toProtoAssetiaPreviewMetadata(m *AssetiaPreviewMetadata) (*assetiapb.GeneratePreviewMetadata, error) {
	source, err := toProtoAssetiaMediaMetadata(&m.Source)
	if err != nil {
		return nil, err
	}

	result, err := toProtoAssetiaImageMetadata(&m.Result)
	if err != nil {
		return nil, err
	}

	return &assetiapb.GeneratePreviewMetadata{
		ProcessingTimeMs: m.ProcessingTime.Milliseconds(),
		Source:           source,
		Result:           result,
	}, nil
}
