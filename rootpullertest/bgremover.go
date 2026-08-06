package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/bgremover"
	"github.com/entwico/rootpuller-sdk/common"
	bgremoverpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/bgremover"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/bgremover/bgremoverconnect"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
)

// BgRemover is a facade-typed fake BackgroundRemoverService. It enforces
// the wire protocol strictly (params frame first, chunks ≤2 MiB, no work
// before the client half-closes) so SDK regressions surface as test
// failures. RemoveFunc receives the reassembled input; a nil RemoveFunc
// echoes the input with a "nobg-" name prefix. When Metadata is set it is
// streamed as the metadata frame ahead of the file chunks, like the real
// server.
type BgRemover struct {
	RemoveFunc func(image common.File) (*common.File, error)
	Metadata   *bgremover.Metadata
}

func (f *BgRemover) register(mux *http.ServeMux) {
	mux.Handle(bgremoverconnect.NewBackgroundRemoverServiceHandler(&bgRemoverHandler{fake: f}))
}

type bgRemoverHandler struct {
	fake *BgRemover
}

func (h *bgRemoverHandler) RemoveBackground(_ context.Context, stream *connect.BidiStream[bgremoverpb.RemoveBackgroundRequest, bgremoverpb.RemoveBackgroundResponse]) error {
	var (
		params *bgremoverpb.RemoveBackgroundRequest_Params
		input  common.File
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
		case *bgremoverpb.RemoveBackgroundRequest_Params_:
			if params != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *bgremoverpb.RemoveBackgroundRequest_File:
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

	remove := h.fake.RemoveFunc
	if remove == nil {
		remove = func(image common.File) (*common.File, error) {
			return &common.File{Name: "nobg-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		}
	}

	result, err := remove(input)
	if err != nil {
		return err
	}

	if m := h.fake.Metadata; m != nil {
		if err := stream.Send(&bgremoverpb.RemoveBackgroundResponse{
			Response: &bgremoverpb.RemoveBackgroundResponse_Metadata{Metadata: toProtoBgRemoverMetadata(m)},
		}); err != nil {
			return err
		}
	}

	return SendFileChunks(result, func(chunk *commonpb.FileChunk) error {
		return stream.Send(&bgremoverpb.RemoveBackgroundResponse{
			Response: &bgremoverpb.RemoveBackgroundResponse_File{File: chunk},
		})
	})
}

func toProtoBgRemoverMetadata(m *bgremover.Metadata) *bgremoverpb.RemoveBackgroundMetadata {
	pb := &bgremoverpb.RemoveBackgroundMetadata{
		ProcessingTimeMs:  m.ProcessingTime.Milliseconds(),
		Size:              toProtoImageSize(m.Size),
		Device:            m.Device,
		MaskConfidence:    m.MaskConfidence,
		ForegroundPercent: m.ForegroundPercent,
	}
	if m.BoundingBox != nil {
		pb.BoundingBox = toProtoBoundingBox(*m.BoundingBox)
	}

	return pb
}

func toProtoImageSize(s common.ImageSize) *commonpb.ImageSize {
	return &commonpb.ImageSize{Width: int32(s.Width), Height: int32(s.Height)} //nolint:gosec // test-fixture image dimensions fit int32
}

func toProtoPoint(p common.Point) *commonpb.Point {
	return &commonpb.Point{X: p.X, Y: p.Y}
}

func toProtoBoundingBox(b common.BoundingBox) *commonpb.BoundingBox {
	return &commonpb.BoundingBox{Position: toProtoPoint(b.Position), Size: toProtoImageSize(b.Size)}
}
