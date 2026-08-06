package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/facefixer"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	facefixerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/facefixer"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/facefixer/facefixerconnect"
)

// FaceFixer is a facade-typed fake FaceFixerService. It enforces the wire
// protocol strictly (params frame first, chunks ≤2 MiB, no work before
// the client half-closes) so SDK regressions surface as test failures.
// FixFunc receives the operation name ("restore", "colorize", or
// "inpaint"), the reassembled input image, and — for inpaint — the
// reassembled mask; a nil FixFunc echoes the input with an "<op>-" name
// prefix. When Metadata is set it is streamed as the metadata frame ahead
// of the file chunks, like the real server.
type FaceFixer struct {
	FixFunc  func(op string, image rootpullersdk.File, mask *rootpullersdk.File) (*rootpullersdk.File, error)
	Metadata *facefixer.Metadata
}

func (f *FaceFixer) register(mux *http.ServeMux) {
	mux.Handle(facefixerconnect.NewFaceFixerServiceHandler(&faceFixerHandler{fake: f}))
}

type faceFixerHandler struct {
	fake *FaceFixer
}

func (h *faceFixerHandler) RestoreFace(_ context.Context, stream *connect.BidiStream[facefixerpb.RestoreFaceRequest, facefixerpb.RestoreFaceResponse]) error {
	var (
		params *facefixerpb.RestoreFaceRequest_Params
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
		case *facefixerpb.RestoreFaceRequest_Params_:
			if params != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *facefixerpb.RestoreFaceRequest_File:
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

	return h.respond("restore", input, nil,
		func(m *facefixerpb.RestoreMetadata) error {
			return stream.Send(&facefixerpb.RestoreFaceResponse{
				Response: &facefixerpb.RestoreFaceResponse_Metadata{Metadata: m},
			})
		},
		func(chunk *commonpb.FileChunk) error {
			return stream.Send(&facefixerpb.RestoreFaceResponse{
				Response: &facefixerpb.RestoreFaceResponse_File{File: chunk},
			})
		},
	)
}

func (h *faceFixerHandler) ColorizeFace(_ context.Context, stream *connect.BidiStream[facefixerpb.ColorizeFaceRequest, facefixerpb.ColorizeFaceResponse]) error {
	var (
		params *facefixerpb.ColorizeFaceRequest_Params
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
		case *facefixerpb.ColorizeFaceRequest_Params_:
			if params != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *facefixerpb.ColorizeFaceRequest_File:
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

	return h.respond("colorize", input, nil,
		func(m *facefixerpb.RestoreMetadata) error {
			return stream.Send(&facefixerpb.ColorizeFaceResponse{
				Response: &facefixerpb.ColorizeFaceResponse_Metadata{Metadata: m},
			})
		},
		func(chunk *commonpb.FileChunk) error {
			return stream.Send(&facefixerpb.ColorizeFaceResponse{
				Response: &facefixerpb.ColorizeFaceResponse_File{File: chunk},
			})
		},
	)
}

func (h *faceFixerHandler) InpaintFace(_ context.Context, stream *connect.BidiStream[facefixerpb.InpaintFaceRequest, facefixerpb.InpaintFaceResponse]) error {
	var (
		params      *facefixerpb.InpaintFaceRequest_Params
		input, mask rootpullersdk.File
	)

	maskSeen := false
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
		case *facefixerpb.InpaintFaceRequest_Params_:
			if params != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *facefixerpb.InpaintFaceRequest_File:
			if params == nil {
				return errBeforeParams("file")
			}

			if err := appendChunk(&input, r.File); err != nil {
				return err
			}
		case *facefixerpb.InpaintFaceRequest_MaskFile:
			if params == nil {
				return errBeforeParams("mask_file")
			}

			if err := appendChunk(&mask, r.MaskFile); err != nil {
				return err
			}

			maskSeen = true
		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if params == nil {
		return invalidArgument(errMissingParams)
	}

	var maskFile *rootpullersdk.File
	if maskSeen {
		maskFile = &mask
	}

	return h.respond("inpaint", input, maskFile,
		func(m *facefixerpb.RestoreMetadata) error {
			return stream.Send(&facefixerpb.InpaintFaceResponse{
				Response: &facefixerpb.InpaintFaceResponse_Metadata{Metadata: m},
			})
		},
		func(chunk *commonpb.FileChunk) error {
			return stream.Send(&facefixerpb.InpaintFaceResponse{
				Response: &facefixerpb.InpaintFaceResponse_File{File: chunk},
			})
		},
	)
}

// respond runs the hook and streams the metadata frame (when configured)
// followed by the result file, using the given per-RPC senders.
func (h *faceFixerHandler) respond(
	op string,
	image rootpullersdk.File,
	mask *rootpullersdk.File,
	sendMetadata func(*facefixerpb.RestoreMetadata) error,
	sendFile func(*commonpb.FileChunk) error,
) error {
	fix := h.fake.FixFunc
	if fix == nil {
		fix = func(op string, image rootpullersdk.File, _ *rootpullersdk.File) (*rootpullersdk.File, error) {
			return &rootpullersdk.File{Name: op + "-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		}
	}

	result, err := fix(op, image, mask)
	if err != nil {
		return err
	}

	if m := h.fake.Metadata; m != nil {
		if err := sendMetadata(toProtoFaceFixerMetadata(m)); err != nil {
			return err
		}
	}

	return SendFileChunks(result, sendFile)
}

var faceFixerOperationToProto = map[string]facefixerpb.Operation{
	"":         facefixerpb.Operation_OPERATION_UNSPECIFIED,
	"restore":  facefixerpb.Operation_OPERATION_RESTORE,
	"colorize": facefixerpb.Operation_OPERATION_COLORIZE,
	"inpaint":  facefixerpb.Operation_OPERATION_INPAINT,
}

func toProtoFaceFixerMetadata(m *facefixer.Metadata) *facefixerpb.RestoreMetadata {
	pb := &facefixerpb.RestoreMetadata{
		FacesDetectedTotal: int32(m.FacesDetected), //nolint:gosec // test-fixture counts fit int32
		UpscaleFactor:      int32(m.UpscaleFactor), //nolint:gosec // test-fixture factors fit int32
		FidelityWeight:     m.FidelityWeight,
		OnlyCenterFace:     m.OnlyCenterFace,
		EnhanceBackground:  m.EnhanceBackground,
		ProcessingTimeMs:   m.ProcessingTime.Milliseconds(),
		InputSize:          toProtoImageSize(m.InputSize),
		OutputSize:         toProtoImageSize(m.OutputSize),
		Model:              m.Model,
		Operation:          faceFixerOperationToProto[m.Operation],
	}
	for _, face := range m.Faces {
		pb.Faces = append(pb.Faces, toProtoFaceInfo(face))
	}

	if m.MaskProvided != nil {
		v := *m.MaskProvided
		pb.MaskProvided = &v
	}

	return pb
}

func toProtoFaceInfo(f facefixer.FaceInfo) *facefixerpb.FaceInfo {
	pb := &facefixerpb.FaceInfo{
		Original: &facefixerpb.FaceInfo_Original{
			Bbox:       toProtoBoundingBox(f.OriginalBBox),
			Confidence: f.Confidence,
		},
	}
	if lm := f.Landmarks; lm != nil {
		pb.Original.Landmarks = &facefixerpb.Landmarks{
			LeftEye:    toProtoPoint(lm.LeftEye),
			RightEye:   toProtoPoint(lm.RightEye),
			Nose:       toProtoPoint(lm.Nose),
			LeftMouth:  toProtoPoint(lm.LeftMouth),
			RightMouth: toProtoPoint(lm.RightMouth),
		}
	}

	if f.RestoredBBox != nil {
		pb.Restored = &facefixerpb.FaceInfo_Restored{Bbox: toProtoBoundingBox(*f.RestoredBBox)}
	}

	return pb
}
