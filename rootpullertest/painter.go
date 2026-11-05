package rootpullertest

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
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
// mask_image; GenerateImage accepts exactly one params frame followed only
// by reference_image chunks; chunks are capped at 2 MiB. Any violation fails the call with
// InvalidArgument, so SDK ordering regressions surface as test failures.
//
// A nil hook responds with one canned 1-byte PNG named "image_0.png".
// Progress frames are streamed first, then the metadata frame (built from
// Metadata, or synthesized from the result files when nil), then the
// files, chunked like the real server.
type Painter struct {
	GenerateFunc func(prompt string) ([]rootpullersdk.File, error)
	// GenerateRequestFunc, when set, replaces GenerateFunc and also sees the
	// reference images (one File per reference, in order) and the
	// comfyui-deployment header the client sent.
	GenerateRequestFunc func(req GenerateRequest) ([]rootpullersdk.File, error)
	EditFunc            func(prompt string, image rootpullersdk.File, mask *rootpullersdk.File) ([]rootpullersdk.File, error)
	// EditRequestFunc, when set, replaces EditFunc and also sees the backend,
	// the model and the comfy sampling options.
	EditRequestFunc func(req EditRequest) ([]rootpullersdk.File, error)
	OutpaintFunc    func(prompt string, image rootpullersdk.File) ([]rootpullersdk.File, error)
	// RefineFacesFunc sees the whole RefineFaces request; RefineFaces
	// accepts input_image chunks only after params and reference_image
	// chunks only after the input.
	RefineFacesFunc func(req RefineFacesRequest) ([]rootpullersdk.File, error)
	Metadata        *painter.Metadata
	Progress        []painter.Progress
}

func (f *Painter) register(mux *http.ServeMux) {
	mux.Handle(painterconnect.NewImagePainterServiceHandler(&painterHandler{fake: f}))
}

type painterHandler struct {
	fake *Painter
}

const painterMaxChunk = 2 << 20

// Painter-specific protocol-violation sentinels.
var (
	errInputImageAfterMask  = errors.New("input_image frame after mask_image")
	errMaskImageBeforeInput = errors.New("mask_image frame before input_image")
	errMissingInputImage    = errors.New("missing input_image")

	errInputImageAfterReference = errors.New("input_image frame after reference_image")
	errReferenceBeforeInput     = errors.New("reference_image frame before input_image")
)

// appendChunk validates one incoming FileChunk and folds it into f.
func appendChunk(f *rootpullersdk.File, chunk *commonpb.FileChunk) error {
	if len(chunk.GetData()) > painterMaxChunk {
		return invalidArgument(errChunkTooLarge)
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

// GenerateRequest is what the fake received on one GenerateImage call.
type GenerateRequest struct {
	Prompt            string
	Backend           string
	Model             string
	References        []rootpullersdk.File
	ComfyUIDeployment string
	// NegativePrompt and GuidanceScale are the comfy backend's; empty and
	// nil otherwise.
	NegativePrompt string
	GuidanceScale  *float64
}

// EditRequest is what the fake received on one EditImage call.
type EditRequest struct {
	Prompt  string
	Backend string
	Model   string
	Image   rootpullersdk.File
	Mask    *rootpullersdk.File
	// NegativePrompt and GuidanceScale are the comfy backend's; empty and
	// nil otherwise.
	NegativePrompt string
	GuidanceScale  *float64
}

// RefineFacesRequest is what a RefineFaces call sent, in facade types.
type RefineFacesRequest struct {
	Prompt            string
	Model             string
	Image             rootpullersdk.File
	References        []rootpullersdk.File
	SearchRegions     []painter.Region
	FaceBoxes         []painter.Region
	ComfyUIDeployment string
}

func (h *painterHandler) GenerateImage(_ context.Context, stream *connect.BidiStream[painterpb.GenerateImageRequest, painterpb.GenerateImageResponse]) error {
	var (
		params     *painterpb.GenerateImageRequest_Params
		references []rootpullersdk.File
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
		case *painterpb.GenerateImageRequest_Params_:
			if params != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *painterpb.GenerateImageRequest_ReferenceImage:
			if params == nil {
				return errBeforeParams("reference_image")
			}

			// a new chunk name starts the next reference, as on the real server.
			if len(references) == 0 || r.ReferenceImage.GetName() != references[len(references)-1].Name {
				references = append(references, rootpullersdk.File{})
			}

			if err := appendChunk(&references[len(references)-1], r.ReferenceImage); err != nil {
				return err
			}
		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if params == nil {
		return invalidArgument(errMissingParams)
	}

	files, err := h.generate(params, references, stream.RequestHeader().Get("Comfyui-Deployment"))
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

// editImageRequest holds the frames accumulated from one EditImage
// request stream. mask stays nil when the client sent no mask_image
// chunks.
type editImageRequest struct {
	params      *painterpb.EditImageRequest_Params
	input       rootpullersdk.File
	mask        *rootpullersdk.File
	inputChunks int
}

// receiveEditImageRequest drains an EditImage request stream, enforcing
// the params → input_image → mask_image frame ordering.
func receiveEditImageRequest(stream *connect.BidiStream[painterpb.EditImageRequest, painterpb.EditImageResponse]) (*editImageRequest, error) {
	acc := &editImageRequest{}

	var (
		maskFile   rootpullersdk.File
		maskChunks int
	)

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, err
		}

		switch r := req.GetRequest().(type) {
		case *painterpb.EditImageRequest_Params_:
			if acc.params != nil {
				return nil, invalidArgument(errDuplicateParamsFrame)
			}

			acc.params = r.Params
		case *painterpb.EditImageRequest_InputImage:
			if acc.params == nil {
				return nil, errBeforeParams("input_image")
			}

			if maskChunks > 0 {
				return nil, invalidArgument(errInputImageAfterMask)
			}

			if err := appendChunk(&acc.input, r.InputImage); err != nil {
				return nil, err
			}

			acc.inputChunks++
		case *painterpb.EditImageRequest_MaskImage:
			if acc.inputChunks == 0 {
				return nil, invalidArgument(errMaskImageBeforeInput)
			}

			if err := appendChunk(&maskFile, r.MaskImage); err != nil {
				return nil, err
			}

			maskChunks++
		default:
			return nil, invalidArgument(errUnexpectedVariant)
		}
	}

	if maskChunks > 0 {
		acc.mask = &maskFile
	}

	return acc, nil
}

func (h *painterHandler) EditImage(_ context.Context, stream *connect.BidiStream[painterpb.EditImageRequest, painterpb.EditImageResponse]) error {
	// Like the real server: drain the full request before any work.
	frames, err := receiveEditImageRequest(stream)
	if err != nil {
		return err
	}

	if frames.params == nil {
		return invalidArgument(errMissingParams)
	}

	if frames.inputChunks == 0 {
		return invalidArgument(errMissingInputImage)
	}

	files, err := h.edit(frames.params, frames.input, frames.mask)
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

func (h *painterHandler) RefineFaces(_ context.Context, stream *connect.BidiStream[painterpb.RefineFacesRequest, painterpb.RefineFacesResponse]) error {
	// like the real server: drain the full request before any work.
	req, err := receiveRefineFacesRequest(stream)
	if err != nil {
		return err
	}

	refine := h.fake.RefineFacesFunc
	if refine == nil {
		refine = func(RefineFacesRequest) ([]rootpullersdk.File, error) { return cannedPainterImages(), nil }
	}

	files, err := refine(req)
	if err != nil {
		return err
	}

	return painterRespond(h.fake, files,
		func(m *painterpb.GenerationProgress) *painterpb.RefineFacesResponse {
			return &painterpb.RefineFacesResponse{Response: &painterpb.RefineFacesResponse_Progress{Progress: m}}
		},
		func(m *painterpb.GenerationMetadata) *painterpb.RefineFacesResponse {
			return &painterpb.RefineFacesResponse{Response: &painterpb.RefineFacesResponse_Metadata{Metadata: m}}
		},
		func(c *commonpb.FileChunk) *painterpb.RefineFacesResponse {
			return &painterpb.RefineFacesResponse{Response: &painterpb.RefineFacesResponse_File{File: c}}
		},
		stream.Send,
	)
}

func receiveRefineFacesRequest(
	stream *connect.BidiStream[painterpb.RefineFacesRequest, painterpb.RefineFacesResponse],
) (RefineFacesRequest, error) {
	var (
		params      *painterpb.RefineFacesRequest_Params
		req         RefineFacesRequest
		inputChunks int
	)

	for {
		msg, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return req, err
		}

		switch r := msg.GetRequest().(type) {
		case *painterpb.RefineFacesRequest_Params_:
			if params != nil {
				return req, invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *painterpb.RefineFacesRequest_InputImage:
			if err := checkRefineInput(params, len(req.References)); err != nil {
				return req, err
			}

			if err := appendChunk(&req.Image, r.InputImage); err != nil {
				return req, err
			}

			inputChunks++
		case *painterpb.RefineFacesRequest_ReferenceImage:
			if inputChunks == 0 {
				return req, invalidArgument(errReferenceBeforeInput)
			}

			if err := appendReferenceChunk(&req.References, r.ReferenceImage); err != nil {
				return req, err
			}
		default:
			return req, invalidArgument(errUnexpectedVariant)
		}
	}

	switch {
	case params == nil:
		return req, invalidArgument(errMissingParams)
	case inputChunks == 0:
		return req, invalidArgument(errMissingInputImage)
	}

	req.Prompt = params.GetPrompt()
	req.Model = params.GetComfy().GetModel()
	req.SearchRegions = fakeRegions(params.GetSearchRegions())
	req.FaceBoxes = fakeRegions(params.GetFaceBoxes())
	req.ComfyUIDeployment = stream.RequestHeader().Get("Comfyui-Deployment")

	return req, nil
}

func checkRefineInput(params *painterpb.RefineFacesRequest_Params, references int) error {
	if params == nil {
		return errBeforeParams("input_image")
	}

	if references > 0 {
		return invalidArgument(errInputImageAfterReference)
	}

	return nil
}

// appendReferenceChunk folds a chunk into the current reference; a new chunk
// name starts the next one, as on the real server.
func appendReferenceChunk(refs *[]rootpullersdk.File, chunk *commonpb.FileChunk) error {
	if len(*refs) == 0 || chunk.GetName() != (*refs)[len(*refs)-1].Name {
		*refs = append(*refs, rootpullersdk.File{})
	}

	return appendChunk(&(*refs)[len(*refs)-1], chunk)
}

func fakeRegions(in []*painterpb.NormalizedRegion) []painter.Region {
	if len(in) == 0 {
		return nil
	}

	out := make([]painter.Region, len(in))
	for i, r := range in {
		out[i] = painter.Region{
			X: float64(r.GetX()), Y: float64(r.GetY()), Width: float64(r.GetWidth()), Height: float64(r.GetHeight()),
		}
	}

	return out
}

func (h *painterHandler) OutpaintImage(_ context.Context, stream *connect.BidiStream[painterpb.OutpaintImageRequest, painterpb.OutpaintImageResponse]) error {
	var (
		params      *painterpb.OutpaintImageRequest_Params
		input       rootpullersdk.File
		inputChunks int
	)

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
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *painterpb.OutpaintImageRequest_InputImage:
			if params == nil {
				return errBeforeParams("input_image")
			}

			if err := appendChunk(&input, r.InputImage); err != nil {
				return err
			}

			inputChunks++
		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if params == nil {
		return invalidArgument(errMissingParams)
	}

	if inputChunks == 0 {
		return invalidArgument(errMissingInputImage)
	}

	outpaint := h.fake.OutpaintFunc
	if outpaint == nil {
		outpaint = func(string, rootpullersdk.File) ([]rootpullersdk.File, error) { return cannedPainterImages(), nil }
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

func (h *painterHandler) generate(
	params *painterpb.GenerateImageRequest_Params,
	references []rootpullersdk.File,
	deployment string,
) ([]rootpullersdk.File, error) {
	if c := params.GetComfy(); c != nil {
		if err := comfySampling(c.GetModel(), c.GetNegativePrompt(), c.GuidanceScale); err != nil {
			return nil, err
		}
	}

	if h.fake.GenerateRequestFunc != nil {
		req := GenerateRequest{
			Prompt:            params.GetPrompt(),
			References:        references,
			ComfyUIDeployment: deployment,
		}

		switch b := params.GetBackend().(type) {
		case *painterpb.GenerateImageRequest_Params_Google:
			req.Backend, req.Model = painter.BackendNameGoogle, b.Google.GetModel()
		case *painterpb.GenerateImageRequest_Params_Openai:
			req.Backend, req.Model = painter.BackendNameOpenAI, b.Openai.GetModel()
		case *painterpb.GenerateImageRequest_Params_Comfy:
			req.Backend, req.Model = painter.BackendNameComfy, b.Comfy.GetModel()
			req.NegativePrompt = b.Comfy.GetNegativePrompt()
			req.GuidanceScale = roundedGuidance(b.Comfy.GuidanceScale)
		}

		return h.fake.GenerateRequestFunc(req)
	}

	if h.fake.GenerateFunc != nil {
		return h.fake.GenerateFunc(params.GetPrompt())
	}

	return cannedPainterImages(), nil
}

// painterRespond streams the response the way the real server does:
// progress frames, then exactly one metadata frame, then one FileChunk
// stream per output image.
func painterRespond[Resp any](
	fake *Painter,
	files []rootpullersdk.File,
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
		err := SendFileChunks(&files[i], func(chunk *commonpb.FileChunk) error {
			return send(wrapFile(chunk))
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// cannedPainterImages is the default result of nil hooks.
func cannedPainterImages() []rootpullersdk.File {
	return []rootpullersdk.File{{Name: "image_0.png", MIMEType: "image/png", Data: []byte{0x89}}}
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
func painterMetadataToProto(m *painter.Metadata, files []rootpullersdk.File) *painterpb.GenerationMetadata {
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
			Index:         int32(im.Index), //nolint:gosec // test-fixture indexes fit int32
			Seed:          im.Seed,
			SafetyBlocked: im.SafetyBlocked,
			SafetyReason:  im.SafetyReason,
			RevisedPrompt: im.RevisedPrompt,
		}
		if im.Size != (rootpullersdk.ImageSize{}) {
			pb.Size = &commonpb.ImageSize{Width: int32(im.Size.Width), Height: int32(im.Size.Height)} //nolint:gosec // test-fixture image dimensions fit int32
		}

		images[i] = pb
	}

	msg := &painterpb.GenerationMetadata{
		Backend:          painterBackendToProto[m.Backend],
		Model:            m.Model,
		ProcessingTimeMs: m.ProcessingTime.Milliseconds(),
		NumImages:        int32(m.NumImages), //nolint:gosec // test-fixture counts fit int32
		Images:           images,
		Notes:            m.Notes,
	}

	for _, r := range m.Faces {
		msg.Faces = append(msg.Faces, &painterpb.NormalizedRegion{
			X: float32(r.X), Y: float32(r.Y), Width: float32(r.Width), Height: float32(r.Height),
		})
	}

	if m.Usage != (rootpullersdk.Usage{}) {
		msg.Usage = &commonpb.Usage{
			InputTokens:             int32(m.Usage.InputTokens),       //nolint:gosec // test-fixture token counts fit int32
			CachedInputTokens:       int32(m.Usage.CachedInputTokens), //nolint:gosec // test-fixture token counts fit int32
			OutputTokens:            int32(m.Usage.OutputTokens),      //nolint:gosec // test-fixture token counts fit int32
			EstimatedCostMicros:     m.Usage.EstimatedCostMicros,
			CurrencyCode:            m.Usage.CurrencyCode,
			InputPricePerMtok:       m.Usage.InputPricePerMTok,
			OutputPricePerMtok:      m.Usage.OutputPricePerMTok,
			CachedInputPricePerMtok: m.Usage.CachedInputPricePerMTok,
		}
	}

	return msg
}

func (h *painterHandler) edit(
	params *painterpb.EditImageRequest_Params,
	image rootpullersdk.File,
	mask *rootpullersdk.File,
) ([]rootpullersdk.File, error) {
	if c := params.GetComfy(); c != nil {
		if err := comfySampling(c.GetModel(), c.GetNegativePrompt(), c.GuidanceScale); err != nil {
			return nil, err
		}
	}

	if h.fake.EditRequestFunc != nil {
		req := EditRequest{Prompt: params.GetPrompt(), Image: image, Mask: mask}

		switch b := params.GetBackend().(type) {
		case *painterpb.EditImageRequest_Params_Google:
			req.Backend, req.Model = painter.BackendNameGoogle, b.Google.GetModel()
		case *painterpb.EditImageRequest_Params_Openai:
			req.Backend, req.Model = painter.BackendNameOpenAI, b.Openai.GetModel()
		case *painterpb.EditImageRequest_Params_Comfy:
			req.Backend, req.Model = painter.BackendNameComfy, b.Comfy.GetModel()
			req.NegativePrompt = b.Comfy.GetNegativePrompt()
			req.GuidanceScale = roundedGuidance(b.Comfy.GuidanceScale)
		}

		return h.fake.EditRequestFunc(req)
	}

	if h.fake.EditFunc != nil {
		return h.fake.EditFunc(params.GetPrompt(), image, mask)
	}

	return cannedPainterImages(), nil
}

// comfyDefaultGuidance is each guided comfy model's guidance as the server
// samples it.
var comfyDefaultGuidance = map[string]float64{
	painter.ComfyModelQwenImage: 1,
	painter.ComfyModelMOSSImage: 3,
}

// comfyMaxNegativeChars is the server's cap on the negative prompt.
const comfyMaxNegativeChars = 2000

// roundedGuidance widens the wire's float32 the way the server does: rounded
// to 3 decimals, so 3.1 stays 3.1.
func roundedGuidance(v *float32) *float64 {
	if v == nil {
		return nil
	}

	return new(math.Round(float64(*v)*1000) / 1000)
}

var (
	errComfyUnguided      = errors.New("the model is distilled to sample without guidance; it takes no negative_prompt or guidance_scale")
	errComfyGuidanceRange = errors.New("guidance_scale must be within 1-10")
	errComfyNegativeAtOne = errors.New("negative_prompt needs guidance_scale above 1")
	errComfyNegativeLong  = errors.New("negative_prompt is longer than 2000 characters")
)

// comfySampling is the server's check of the comfy negative prompt and
// guidance. A model the fake does not know is left to the hooks, as the
// server's answer for it depends on what the deployment serves.
func comfySampling(model, negative string, guidance *float32) error {
	negative = strings.TrimSpace(negative)

	if negative == "" && guidance == nil {
		return nil
	}

	effective, guided := comfyDefaultGuidance[model]
	if !guided {
		if model == painter.ComfyModelKrea2Turbo {
			return invalidArgument(errComfyUnguided)
		}

		return nil
	}

	if utf8.RuneCountInString(negative) > comfyMaxNegativeChars {
		return invalidArgument(errComfyNegativeLong)
	}

	if guidance != nil {
		g := *roundedGuidance(guidance)

		if !(g >= 1 && g <= 10) {
			return invalidArgument(errComfyGuidanceRange)
		}

		effective = g
	}

	if negative != "" && effective <= 1 {
		return invalidArgument(errComfyNegativeAtOne)
	}

	return nil
}
