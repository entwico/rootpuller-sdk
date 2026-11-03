package painter

import (
	painterpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/painter"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// Models the server runs on its ComfyUI deployments. A deployment serves a
// subset; asking for a model it does not serve fails with NotFound.
const (
	// ComfyModelQwenImage takes up to 7 reference images and supports Edit.
	ComfyModelQwenImage = "qwen-image-2.1"
	// ComfyModelMOSSImage is text-only and renders readable text well.
	ComfyModelMOSSImage = "moss-image"
	// ComfyModelKrea2Turbo is text-only and fast, for drafts.
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
	}, nil
}
