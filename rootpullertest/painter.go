package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/common"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	painterpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/painter"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/painter/painterconnect"
	"github.com/entwico/rootpuller-sdk/painter"
)

// Painter is a facade-typed fake ImagePainterService. It enforces the
// wire protocol strictly, mirroring the real server's request state
// machine: the params frame must come first and exactly once; EditImage
// accepts input_image chunks only after params, mask_image chunks only
// after at least one input_image chunk, and no input_image after
// mask_image; GenerateImage accepts exactly one params frame and nothing
// else; chunks are capped at 2 MiB. Any violation fails the call with
// InvalidArgument, so SDK ordering regressions surface as test failures.
//
// A nil hook responds with one canned 1-byte PNG named "image_0.png".
// Progress frames are streamed first, then the metadata frame (built from
// Metadata, or synthesized from the result files when nil), then the
// files, chunked like the real server.
type Painter struct {
	GenerateFunc func(prompt string) ([]common.File, error)
	EditFunc     func(prompt string, image common.File, mask *common.File) ([]common.File, error)
	OutpaintFunc func(prompt string, image common.File) ([]common.File, error)
	Metadata     *painter.Metadata
	Progress     []painter.Progress
}

func (f *Painter) register(mux *http.ServeMux) {
	mux.Handle(painterconnect.NewImagePainterServiceHandler(&painterHandler{fake: f}))
}

type painterHandler struct {
	fake *Painter
}

const painterMaxChunk = 2 << 20

func painterInvalid(msg string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
}

// appendChunk validates one incoming FileChunk and folds it into f.
func appendChunk(f *common.File, chunk *commonpb.FileChunk) error {
	if len(chunk.GetData()) > painterMaxChunk {
		return painterInvalid("chunk exceeds 2 MiB")
	}
	if f.Name == "" {
		f.Name = chunk.GetName()
	}
	if f.MIMEType == "" {
		f.MIMEType = chunk.GetMimeType()
	}
	f.Data = append(f.Data, chunk.GetData()...)
	return nil
}

func (h *painterHandler) GenerateImage(_ context.Context, stream *connect.BidiStream[painterpb.GenerateImageRequest, painterpb.GenerateImageResponse]) error {
	var params *painterpb.GenerateImageRequest_Params
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
		case *painterpb.GenerateImageRequest_Params_:
			if params != nil {
				return painterInvalid("duplicate params frame")
			}
			params = r.Params
		default:
			return painterInvalid("unexpected request variant")
		}
	}
	if params == nil {
		return painterInvalid("missing params")
	}

	generate := h.fake.GenerateFunc
	if generate == nil {
		generate = func(string) ([]common.File, error) { return cannedPainterImages(), nil }
	}
	files, err := generate(params.GetPrompt())
	if err != nil {
		return err
	}
	return painterRespond(h.fake, files,
		func(m *painterpb.GenerationProgress) *painterpb.GenerateImageResponse {
			return &painterpb.GenerateImageResponse{Response: &painterpb.GenerateImageResponse_Progress{Progress: m}}
		},
		func(m *painterpb.GenerationMetadata) *painterpb.GenerateImageResponse {
			return &painterpb.GenerateImageResponse{Response: &painterpb.GenerateImageResponse_Metadata{Metadata: m}}
		},
		func(c *commonpb.FileChunk) *painterpb.GenerateImageResponse {
			return &painterpb.GenerateImageResponse{Response: &painterpb.GenerateImageResponse_File{File: c}}
		},
		stream.Send,
	)
}

func (h *painterHandler) EditImage(_ context.Context, stream *connect.BidiStream[painterpb.EditImageRequest, painterpb.EditImageResponse]) error {
	var params *painterpb.EditImageRequest_Params
	var input, mask common.File
	var inputChunks, maskChunks int
	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		switch r := req.GetRequest().(type) {
		case *painterpb.EditImageRequest_Params_:
			if params != nil {
				return painterInvalid("duplicate params frame")
			}
			params = r.Params
		case *painterpb.EditImageRequest_InputImage:
			if params == nil {
				return painterInvalid("input_image frame before params")
			}
			if maskChunks > 0 {
				return painterInvalid("input_image frame after mask_image")
			}
			if err := appendChunk(&input, r.InputImage); err != nil {
				return err
			}
			inputChunks++
		case *painterpb.EditImageRequest_MaskImage:
			if inputChunks == 0 {
				return painterInvalid("mask_image frame before input_image")
			}
			if err := appendChunk(&mask, r.MaskImage); err != nil {
				return err
			}
			maskChunks++
		default:
			return painterInvalid("unexpected request variant")
		}
	}
	if params == nil {
		return painterInvalid("missing params")
	}
	if inputChunks == 0 {
		return painterInvalid("missing input_image")
	}

	edit := h.fake.EditFunc
	if edit == nil {
		edit = func(string, common.File, *common.File) ([]common.File, error) { return cannedPainterImages(), nil }
	}
	var maskArg *common.File
	if maskChunks > 0 {
		maskArg = &mask
	}
	files, err := edit(params.GetPrompt(), input, maskArg)
	if err != nil {
		return err
	}
	return painterRespond(h.fake, files,
		func(m *painterpb.GenerationProgress) *painterpb.EditImageResponse {
			return &painterpb.EditImageResponse{Response: &painterpb.EditImageResponse_Progress{Progress: m}}
		},
		func(m *painterpb.GenerationMetadata) *painterpb.EditImageResponse {
			return &painterpb.EditImageResponse{Response: &painterpb.EditImageResponse_Metadata{Metadata: m}}
		},
		func(c *commonpb.FileChunk) *painterpb.EditImageResponse {
			return &painterpb.EditImageResponse{Response: &painterpb.EditImageResponse_File{File: c}}
		},
		stream.Send,
	)
}

func (h *painterHandler) OutpaintImage(_ context.Context, stream *connect.BidiStream[painterpb.OutpaintImageRequest, painterpb.OutpaintImageResponse]) error {
	var params *painterpb.OutpaintImageRequest_Params
	var input common.File
	var inputChunks int
	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		switch r := req.GetRequest().(type) {
		case *painterpb.OutpaintImageRequest_Params_:
			if params != nil {
				return painterInvalid("duplicate params frame")
			}
			params = r.Params
		case *painterpb.OutpaintImageRequest_InputImage:
			if params == nil {
				return painterInvalid("input_image frame before params")
			}
			if err := appendChunk(&input, r.InputImage); err != nil {
				return err
			}
			inputChunks++
		default:
			return painterInvalid("unexpected request variant")
		}
	}
	if params == nil {
		return painterInvalid("missing params")
	}
	if inputChunks == 0 {
		return painterInvalid("missing input_image")
	}

	outpaint := h.fake.OutpaintFunc
	if outpaint == nil {
		outpaint = func(string, common.File) ([]common.File, error) { return cannedPainterImages(), nil }
	}
	files, err := outpaint(params.GetPrompt(), input)
	if err != nil {
		return err
	}
	return painterRespond(h.fake, files,
		func(m *painterpb.GenerationProgress) *painterpb.OutpaintImageResponse {
			return &painterpb.OutpaintImageResponse{Response: &painterpb.OutpaintImageResponse_Progress{Progress: m}}
		},
		func(m *painterpb.GenerationMetadata) *painterpb.OutpaintImageResponse {
			return &painterpb.OutpaintImageResponse{Response: &painterpb.OutpaintImageResponse_Metadata{Metadata: m}}
		},
		func(c *commonpb.FileChunk) *painterpb.OutpaintImageResponse {
			return &painterpb.OutpaintImageResponse{Response: &painterpb.OutpaintImageResponse_File{File: c}}
		},
		stream.Send,
	)
}

// painterRespond streams the response the way the real server does:
// progress frames, then exactly one metadata frame, then one FileChunk
// stream per output image.
func painterRespond[Resp any](
	fake *Painter,
	files []common.File,
	wrapProgress func(*painterpb.GenerationProgress) *Resp,
	wrapMetadata func(*painterpb.GenerationMetadata) *Resp,
	wrapFile func(*commonpb.FileChunk) *Resp,
	send func(*Resp) error,
) error {
	for _, p := range fake.Progress {
		if err := send(wrapProgress(painterProgressToProto(p))); err != nil {
			return err
		}
	}
	if err := send(wrapMetadata(painterMetadataToProto(fake.Metadata, files))); err != nil {
		return err
	}
	for i := range files {
		if err := SendFileChunks(&files[i], func(chunk *commonpb.FileChunk) error {
			return send(wrapFile(chunk))
		}); err != nil {
			return err
		}
	}
	return nil
}

// cannedPainterImages is the default result of nil hooks.
func cannedPainterImages() []common.File {
	return []common.File{{Name: "image_0.png", MIMEType: "image/png", Data: []byte{0x89}}}
}

var painterStageToProto = map[painter.Stage]painterpb.GenerationProgress_Stage{
	painter.StageUnspecified: painterpb.GenerationProgress_STAGE_UNSPECIFIED,
	painter.StageQueued:      painterpb.GenerationProgress_STAGE_QUEUED,
	painter.StageGenerating:  painterpb.GenerationProgress_STAGE_GENERATING,
	painter.StageSafetyCheck: painterpb.GenerationProgress_STAGE_SAFETY_CHECK,
	painter.StageUploading:   painterpb.GenerationProgress_STAGE_UPLOADING,
}

func painterProgressToProto(p painter.Progress) *painterpb.GenerationProgress {
	msg := &painterpb.GenerationProgress{Stage: painterStageToProto[p.Stage], Percentage: p.Percentage}
	if p.Message != "" {
		message := p.Message
		msg.Message = &message
	}
	return msg
}

var painterBackendToProto = map[string]painterpb.GenerationMetadata_Backend{
	"":       painterpb.GenerationMetadata_BACKEND_UNSPECIFIED,
	"google": painterpb.GenerationMetadata_BACKEND_GOOGLE,
	"openai": painterpb.GenerationMetadata_BACKEND_OPENAI,
}

// painterMetadataToProto converts the fake's facade metadata; when m is
// nil it synthesizes a minimal metadata frame from the result files, so
// the response always carries the protocol's mandatory metadata.
func painterMetadataToProto(m *painter.Metadata, files []common.File) *painterpb.GenerationMetadata {
	if m == nil {
		synth := painter.Metadata{NumImages: len(files), Images: make([]painter.ImageMetadata, len(files))}
		for i := range synth.Images {
			synth.Images[i] = painter.ImageMetadata{Index: i}
		}
		m = &synth
	}
	images := make([]*painterpb.PerImageMetadata, len(m.Images))
	for i, im := range m.Images {
		pb := &painterpb.PerImageMetadata{
			Index:         int32(im.Index),
			Seed:          im.Seed,
			SafetyBlocked: im.SafetyBlocked,
			SafetyReason:  im.SafetyReason,
			RevisedPrompt: im.RevisedPrompt,
		}
		if im.Size != (common.ImageSize{}) {
			pb.Size = &commonpb.ImageSize{Width: int32(im.Size.Width), Height: int32(im.Size.Height)}
		}
		images[i] = pb
	}
	msg := &painterpb.GenerationMetadata{
		Backend:          painterBackendToProto[m.Backend],
		Model:            m.Model,
		ProcessingTimeMs: m.ProcessingTime.Milliseconds(),
		NumImages:        int32(m.NumImages),
		Images:           images,
		Notes:            m.Notes,
	}
	if m.Usage != (common.Usage{}) {
		msg.Usage = &commonpb.Usage{
			InputTokens:             int32(m.Usage.InputTokens),
			CachedInputTokens:       int32(m.Usage.CachedInputTokens),
			OutputTokens:            int32(m.Usage.OutputTokens),
			EstimatedCostMicros:     m.Usage.EstimatedCostMicros,
			CurrencyCode:            m.Usage.CurrencyCode,
			InputPricePerMtok:       m.Usage.InputPricePerMTok,
			OutputPricePerMtok:      m.Usage.OutputPricePerMTok,
			CachedInputPricePerMtok: m.Usage.CachedInputPricePerMTok,
		}
	}
	return msg
}
