package painter

import (
	painterpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/painter"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// Models the server runs on its ComfyUI deployments. A deployment serves a
// subset; asking for a model it does not serve fails with NotFound.
const (
	// ComfyModelQwenImage takes up to 7 reference images and supports Edit
	// and RefineFaces. It samples at guidance 1 by default; raise
	// GuidanceScale to use a NegativePrompt.
	ComfyModelQwenImage = "qwen-image-2.1"
	// ComfyModelMOSSImage is text-only and renders readable text well. It
	// samples at guidance 3 by default.
	ComfyModelMOSSImage = "moss-image"
	// ComfyModelKrea2Turbo is text-only and fast, for drafts. It is distilled
	// to sample without guidance: no NegativePrompt or GuidanceScale.
	ComfyModelKrea2Turbo = "krea-2-turbo"
)

// ComfyOutputFormat selects the output encoding on the comfy backend. The
// zero value keeps the server default (png).
type ComfyOutputFormat string

const (
	ComfyOutputFormatDefault ComfyOutputFormat = ""
	ComfyOutputFormatPNG     ComfyOutputFormat = "png"
	ComfyOutputFormatJPEG    ComfyOutputFormat = "jpeg"
)

var comfyOutputFormatToProto = map[ComfyOutputFormat]painterpb.ComfyOutputFormat{
	ComfyOutputFormatDefault: painterpb.ComfyOutputFormat_COMFY_OUTPUT_FORMAT_UNSPECIFIED,
	ComfyOutputFormatPNG:     painterpb.ComfyOutputFormat_COMFY_OUTPUT_FORMAT_PNG,
	ComfyOutputFormatJPEG:    painterpb.ComfyOutputFormat_COMFY_OUTPUT_FORMAT_JPEG,
}

// GenerateComfy selects the comfy backend for Generate: a self-hosted
// open-weight model on a ComfyUI deployment, picked with
// WithComfyUIDeployment or rootpullersdk.ContextWithComfyUIDeployment.
// Calls can wait in the deployment's queue for a long time; progress
// reports the queue position.
type GenerateComfy struct {
	// Model is required, e.g. ComfyModelMOSSImage.
	Model string
	// NumberOfImages is 1-4. Default: 1. Image i uses Seed + i.
	NumberOfImages *int
	// Seed of the first image. Default: random; the used seeds come back
	// in ImageMetadata.Seed.
	Seed *int64
	// Width and Height are multiples of 16. Default: per model.
	Width        *int
	Height       *int
	OutputFormat ComfyOutputFormat
	// NegativePrompt names what the image must not show, e.g. "text,
	// lettering, logo, duplicate objects", at most 2000 characters. A few
	// targeted terms per failure work better than a long generic list. The model reads it only while
	// guidance is above 1, so it needs GuidanceScale above 1 on
	// ComfyModelQwenImage; ComfyModelKrea2Turbo rejects it.
	NegativePrompt string
	// GuidanceScale is classifier-free guidance, 1-10: how strongly sampling
	// follows the prompt and steers away from NegativePrompt. Nil keeps the
	// model's default. Above 1 sampling takes about twice as long.
	GuidanceScale *float64
}

func (GenerateComfy) isBackend() {}

func (b GenerateComfy) toProto() (*painterpb.GenerateImageRequest_Comfy, error) {
	if b.Model == "" {
		return nil, invalidArgument("GenerateComfy.Model is required")
	}

	format, err := enumToProto(comfyOutputFormatToProto, b.OutputFormat, "output format")
	if err != nil {
		return nil, err
	}

	return &painterpb.GenerateImageRequest_Comfy{
		Model:          b.Model,
		NumberOfImages: protoconv.Int32Ptr(b.NumberOfImages),
		Seed:           b.Seed,
		Width:          protoconv.Int32Ptr(b.Width),
		Height:         protoconv.Int32Ptr(b.Height),
		OutputFormat:   format,
		NegativePrompt: optString(b.NegativePrompt),
		GuidanceScale:  optFloat32(b.GuidanceScale),
	}, nil
}

// EditComfy selects the comfy backend for Edit: a prompt-driven edit that
// keeps the input image's layout. The input image is also <image1> in the
// prompt. Only models that support editing accept it (ComfyModelQwenImage),
// and a mask is not supported.
type EditComfy struct {
	// Model is required, e.g. ComfyModelQwenImage.
	Model string
	// NumberOfImages is 1-4. Default: 1. Image i uses Seed + i.
	NumberOfImages *int
	Seed           *int64
	// Width and Height are multiples of 16. Default: the input image's size.
	Width        *int
	Height       *int
	OutputFormat ComfyOutputFormat
	// NegativePrompt names what the image must not show, e.g. "text,
	// lettering, logo, duplicate objects", at most 2000 characters. A few
	// targeted terms per failure work better than a long generic list. The model reads it only while
	// guidance is above 1, so it needs GuidanceScale above 1 on
	// ComfyModelQwenImage; ComfyModelKrea2Turbo rejects it.
	NegativePrompt string
	// GuidanceScale is classifier-free guidance, 1-10: how strongly sampling
	// follows the prompt and steers away from NegativePrompt. Nil keeps the
	// model's default. Above 1 sampling takes about twice as long.
	GuidanceScale *float64
}

func (EditComfy) isBackend() {}

func (b EditComfy) toProto() (*painterpb.EditImageRequest_Comfy, error) {
	if b.Model == "" {
		return nil, invalidArgument("EditComfy.Model is required")
	}

	format, err := enumToProto(comfyOutputFormatToProto, b.OutputFormat, "output format")
	if err != nil {
		return nil, err
	}

	return &painterpb.EditImageRequest_Comfy{
		Model:          b.Model,
		NumberOfImages: protoconv.Int32Ptr(b.NumberOfImages),
		Seed:           b.Seed,
		Width:          protoconv.Int32Ptr(b.Width),
		Height:         protoconv.Int32Ptr(b.Height),
		OutputFormat:   format,
		NegativePrompt: optString(b.NegativePrompt),
		GuidanceScale:  optFloat32(b.GuidanceScale),
	}, nil
}

// RefineFacesComfy selects the comfy backend for RefineFaces, the only
// backend that refines faces. Only models that support editing accept it
// (ComfyModelQwenImage).
type RefineFacesComfy struct {
	// Model is required, e.g. ComfyModelQwenImage.
	Model string
	// NumberOfImages is 1-4. Default: 1. Image i refines the same faces with
	// Seed + i, so the images differ only in the faces.
	NumberOfImages *int
	Seed           *int64
	OutputFormat   ComfyOutputFormat
	// Denoise is how much of each face crop is redrawn, (0, 1]. Default: 1, a
	// full redraw guided by the crop and the references; this is what adds
	// detail. Lower values keep more of the crop, including its blur.
	Denoise *float64
	// WorkingResolution is the side in pixels each face crop is redrawn at,
	// 512-2048. Default: 1024. A larger crop keeps its own size, up to 2048.
	WorkingResolution *int
}

func (RefineFacesComfy) isBackend() {}

func (b RefineFacesComfy) toProto() (*painterpb.RefineFacesRequest_Comfy, error) {
	if b.Model == "" {
		return nil, invalidArgument("RefineFacesComfy.Model is required")
	}

	format, err := enumToProto(comfyOutputFormatToProto, b.OutputFormat, "output format")
	if err != nil {
		return nil, err
	}

	out := &painterpb.RefineFacesRequest_Comfy{
		Model:             b.Model,
		NumberOfImages:    protoconv.Int32Ptr(b.NumberOfImages),
		Seed:              b.Seed,
		OutputFormat:      format,
		WorkingResolution: protoconv.Int32Ptr(b.WorkingResolution),
	}

	if b.Denoise != nil {
		out.Denoise = new(float32(*b.Denoise))
	}

	return out, nil
}

func optString(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func optFloat32(v *float64) *float32 {
	if v == nil {
		return nil
	}

	return new(float32(*v))
}
