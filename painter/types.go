package painter

import (
	"fmt"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	painterpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/painter"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// enumToProto maps a facade enum value to the optional proto enum field.
// The zero value ("" = server default) maps to nil so the field is
// omitted; unknown values fail with a local InvalidArgument before any
// RPC.
func enumToProto[F ~string, P ~int32](m map[F]P, v F, what string) (*P, error) {
	p, ok := m[v]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown %s %q", what, v))
	}

	if p == 0 {
		return nil, nil
	}

	return &p, nil
}

// enumFromProto tolerates enum values newer than this SDK by falling back
// to the proto identifier string.
func enumFromProto[P interface {
	comparable
	fmt.Stringer
}, F ~string](m map[P]F, v P) F {
	if f, ok := m[v]; ok {
		return f
	}

	return F(v.String())
}

// AspectRatio selects the output aspect ratio on the Google backend. The
// zero value keeps the server default (1:1).
type AspectRatio string

const (
	AspectRatioDefault AspectRatio = ""
	AspectRatio1x1     AspectRatio = "1:1"
	AspectRatio9x16    AspectRatio = "9:16"
	AspectRatio16x9    AspectRatio = "16:9"
	AspectRatio3x4     AspectRatio = "3:4"
	AspectRatio4x3     AspectRatio = "4:3"
)

var aspectRatioToProto = map[AspectRatio]painterpb.GenerateImageRequest_Google_AspectRatio{
	AspectRatioDefault: painterpb.GenerateImageRequest_Google_ASPECT_RATIO_UNSPECIFIED,
	AspectRatio1x1:     painterpb.GenerateImageRequest_Google_ASPECT_RATIO_1_1,
	AspectRatio9x16:    painterpb.GenerateImageRequest_Google_ASPECT_RATIO_9_16,
	AspectRatio16x9:    painterpb.GenerateImageRequest_Google_ASPECT_RATIO_16_9,
	AspectRatio3x4:     painterpb.GenerateImageRequest_Google_ASPECT_RATIO_3_4,
	AspectRatio4x3:     painterpb.GenerateImageRequest_Google_ASPECT_RATIO_4_3,
}

// GoogleSafetyFilterLevel maps to Imagen's safetyFilterLevel parameter.
// The zero value keeps the server default (block-medium-and-above).
// "block-none" requires project-level allowlisting.
type GoogleSafetyFilterLevel string

const (
	GoogleSafetyFilterLevelDefault             GoogleSafetyFilterLevel = ""
	GoogleSafetyFilterLevelBlockLowAndAbove    GoogleSafetyFilterLevel = "block-low-and-above"
	GoogleSafetyFilterLevelBlockMediumAndAbove GoogleSafetyFilterLevel = "block-medium-and-above"
	GoogleSafetyFilterLevelBlockOnlyHigh       GoogleSafetyFilterLevel = "block-only-high"
	GoogleSafetyFilterLevelBlockNone           GoogleSafetyFilterLevel = "block-none"
)

var googleSafetyFilterLevelToProto = map[GoogleSafetyFilterLevel]painterpb.GoogleSafetyFilterLevel{
	GoogleSafetyFilterLevelDefault:             painterpb.GoogleSafetyFilterLevel_GOOGLE_SAFETY_FILTER_LEVEL_UNSPECIFIED,
	GoogleSafetyFilterLevelBlockLowAndAbove:    painterpb.GoogleSafetyFilterLevel_GOOGLE_SAFETY_FILTER_LEVEL_BLOCK_LOW_AND_ABOVE,
	GoogleSafetyFilterLevelBlockMediumAndAbove: painterpb.GoogleSafetyFilterLevel_GOOGLE_SAFETY_FILTER_LEVEL_BLOCK_MEDIUM_AND_ABOVE,
	GoogleSafetyFilterLevelBlockOnlyHigh:       painterpb.GoogleSafetyFilterLevel_GOOGLE_SAFETY_FILTER_LEVEL_BLOCK_ONLY_HIGH,
	GoogleSafetyFilterLevelBlockNone:           painterpb.GoogleSafetyFilterLevel_GOOGLE_SAFETY_FILTER_LEVEL_BLOCK_NONE,
}

// GooglePersonGeneration maps to Imagen's personGeneration parameter. The
// zero value keeps the server default (allow-adult). "allow-all" requires
// project-level allowlisting on Vertex AI.
type GooglePersonGeneration string

const (
	GooglePersonGenerationDefault    GooglePersonGeneration = ""
	GooglePersonGenerationDontAllow  GooglePersonGeneration = "dont-allow"
	GooglePersonGenerationAllowAdult GooglePersonGeneration = "allow-adult"
	GooglePersonGenerationAllowAll   GooglePersonGeneration = "allow-all"
)

var googlePersonGenerationToProto = map[GooglePersonGeneration]painterpb.GooglePersonGeneration{
	GooglePersonGenerationDefault:    painterpb.GooglePersonGeneration_GOOGLE_PERSON_GENERATION_UNSPECIFIED,
	GooglePersonGenerationDontAllow:  painterpb.GooglePersonGeneration_GOOGLE_PERSON_GENERATION_DONT_ALLOW,
	GooglePersonGenerationAllowAdult: painterpb.GooglePersonGeneration_GOOGLE_PERSON_GENERATION_ALLOW_ADULT,
	GooglePersonGenerationAllowAll:   painterpb.GooglePersonGeneration_GOOGLE_PERSON_GENERATION_ALLOW_ALL,
}

// GoogleOutputMIMEType selects the output container on the Google
// backend. The zero value keeps the server default (png). Imagen does not
// emit WebP.
type GoogleOutputMIMEType string

const (
	GoogleOutputMIMETypeDefault GoogleOutputMIMEType = ""
	GoogleOutputMIMETypePNG     GoogleOutputMIMEType = "png"
	GoogleOutputMIMETypeJPEG    GoogleOutputMIMEType = "jpeg"
)

var googleOutputMIMETypeToProto = map[GoogleOutputMIMEType]painterpb.GoogleOutputMimeType{
	GoogleOutputMIMETypeDefault: painterpb.GoogleOutputMimeType_GOOGLE_OUTPUT_MIME_TYPE_UNSPECIFIED,
	GoogleOutputMIMETypePNG:     painterpb.GoogleOutputMimeType_GOOGLE_OUTPUT_MIME_TYPE_PNG,
	GoogleOutputMIMETypeJPEG:    painterpb.GoogleOutputMimeType_GOOGLE_OUTPUT_MIME_TYPE_JPEG,
}

// GoogleEditMode tells the Imagen capability model how to interpret the
// mask during Edit. The zero value keeps the server default
// (inpaint-insertion).
type GoogleEditMode string

const (
	GoogleEditModeDefault          GoogleEditMode = ""
	GoogleEditModeInpaintInsertion GoogleEditMode = "inpaint-insertion"
	GoogleEditModeInpaintRemoval   GoogleEditMode = "inpaint-removal"
	GoogleEditModeProductImage     GoogleEditMode = "product-image"
)

var googleEditModeToProto = map[GoogleEditMode]painterpb.EditImageRequest_Google_EditMode{
	GoogleEditModeDefault:          painterpb.EditImageRequest_Google_EDIT_MODE_UNSPECIFIED,
	GoogleEditModeInpaintInsertion: painterpb.EditImageRequest_Google_EDIT_MODE_INPAINT_INSERTION,
	GoogleEditModeInpaintRemoval:   painterpb.EditImageRequest_Google_EDIT_MODE_INPAINT_REMOVAL,
	GoogleEditModeProductImage:     painterpb.EditImageRequest_Google_EDIT_MODE_PRODUCT_IMAGE,
}

// OpenAISize selects the output image size on the OpenAI backend. The
// zero value keeps the server default (auto).
type OpenAISize string

const (
	OpenAISizeDefault   OpenAISize = ""
	OpenAISizeAuto      OpenAISize = "auto"
	OpenAISize1024x1024 OpenAISize = "1024x1024"
	OpenAISize1024x1536 OpenAISize = "1024x1536" // portrait
	OpenAISize1536x1024 OpenAISize = "1536x1024" // landscape
)

var openAISizeToProto = map[OpenAISize]painterpb.OpenAIImageSize{
	OpenAISizeDefault:   painterpb.OpenAIImageSize_OPEN_AI_IMAGE_SIZE_UNSPECIFIED,
	OpenAISizeAuto:      painterpb.OpenAIImageSize_OPEN_AI_IMAGE_SIZE_AUTO,
	OpenAISize1024x1024: painterpb.OpenAIImageSize_OPEN_AI_IMAGE_SIZE_1024X1024,
	OpenAISize1024x1536: painterpb.OpenAIImageSize_OPEN_AI_IMAGE_SIZE_1024X1536,
	OpenAISize1536x1024: painterpb.OpenAIImageSize_OPEN_AI_IMAGE_SIZE_1536X1024,
}

// OpenAIQuality selects the quality preset on the OpenAI backend. The
// zero value keeps the server default (auto).
type OpenAIQuality string

const (
	OpenAIQualityDefault OpenAIQuality = ""
	OpenAIQualityAuto    OpenAIQuality = "auto"
	OpenAIQualityLow     OpenAIQuality = "low"
	OpenAIQualityMedium  OpenAIQuality = "medium"
	OpenAIQualityHigh    OpenAIQuality = "high"
)

var openAIQualityToProto = map[OpenAIQuality]painterpb.OpenAIQuality{
	OpenAIQualityDefault: painterpb.OpenAIQuality_OPEN_AI_QUALITY_UNSPECIFIED,
	OpenAIQualityAuto:    painterpb.OpenAIQuality_OPEN_AI_QUALITY_AUTO,
	OpenAIQualityLow:     painterpb.OpenAIQuality_OPEN_AI_QUALITY_LOW,
	OpenAIQualityMedium:  painterpb.OpenAIQuality_OPEN_AI_QUALITY_MEDIUM,
	OpenAIQualityHigh:    painterpb.OpenAIQuality_OPEN_AI_QUALITY_HIGH,
}

// OpenAIBackground selects background handling on the OpenAI backend.
// The zero value keeps the server default (auto).
type OpenAIBackground string

const (
	OpenAIBackgroundDefault     OpenAIBackground = ""
	OpenAIBackgroundAuto        OpenAIBackground = "auto"
	OpenAIBackgroundOpaque      OpenAIBackground = "opaque"
	OpenAIBackgroundTransparent OpenAIBackground = "transparent"
)

var openAIBackgroundToProto = map[OpenAIBackground]painterpb.OpenAIBackground{
	OpenAIBackgroundDefault:     painterpb.OpenAIBackground_OPEN_AI_BACKGROUND_UNSPECIFIED,
	OpenAIBackgroundAuto:        painterpb.OpenAIBackground_OPEN_AI_BACKGROUND_AUTO,
	OpenAIBackgroundOpaque:      painterpb.OpenAIBackground_OPEN_AI_BACKGROUND_OPAQUE,
	OpenAIBackgroundTransparent: painterpb.OpenAIBackground_OPEN_AI_BACKGROUND_TRANSPARENT,
}

// OpenAIOutputFormat selects the output encoding on the OpenAI backend.
// The zero value keeps the server default (png).
type OpenAIOutputFormat string

const (
	OpenAIOutputFormatDefault OpenAIOutputFormat = ""
	OpenAIOutputFormatPNG     OpenAIOutputFormat = "png"
	OpenAIOutputFormatJPEG    OpenAIOutputFormat = "jpeg"
	OpenAIOutputFormatWebP    OpenAIOutputFormat = "webp"
)

var openAIOutputFormatToProto = map[OpenAIOutputFormat]painterpb.OpenAIOutputFormat{
	OpenAIOutputFormatDefault: painterpb.OpenAIOutputFormat_OPEN_AI_OUTPUT_FORMAT_UNSPECIFIED,
	OpenAIOutputFormatPNG:     painterpb.OpenAIOutputFormat_OPEN_AI_OUTPUT_FORMAT_PNG,
	OpenAIOutputFormatJPEG:    painterpb.OpenAIOutputFormat_OPEN_AI_OUTPUT_FORMAT_JPEG,
	OpenAIOutputFormatWebP:    painterpb.OpenAIOutputFormat_OPEN_AI_OUTPUT_FORMAT_WEBP,
}

// OpenAIModeration controls content-policy strictness on the OpenAI
// backend (Generate only). The zero value keeps the server default
// (auto).
type OpenAIModeration string

const (
	OpenAIModerationDefault OpenAIModeration = ""
	OpenAIModerationAuto    OpenAIModeration = "auto"
	OpenAIModerationLow     OpenAIModeration = "low"
)

var openAIModerationToProto = map[OpenAIModeration]painterpb.GenerateImageRequest_OpenAI_Moderation{
	OpenAIModerationDefault: painterpb.GenerateImageRequest_OpenAI_MODERATION_UNSPECIFIED,
	OpenAIModerationAuto:    painterpb.GenerateImageRequest_OpenAI_MODERATION_AUTO,
	OpenAIModerationLow:     painterpb.GenerateImageRequest_OpenAI_MODERATION_LOW,
}

// Backend selects the image-generation backend and carries its tuning
// options. It is a sealed interface; the concrete types are per method:
// GenerateGoogle / GenerateOpenAI for Generate, EditGoogle / EditOpenAI
// for Edit, and OutpaintGoogle / OutpaintOpenAI for Outpaint, because the
// option sets differ per RPC. A nil Backend lets the server pick its
// default backend with default options.
type Backend interface{ isBackend() }

// GenerateGoogle selects the Google Imagen backend for Generate. Nil
// pointer and zero enum fields keep the server defaults.
type GenerateGoogle struct {
	// Model overrides the server's SOTA model (e.g.
	// "imagen-3.0-generate-002").
	Model *string
	// NumberOfImages is 1-4. Default: 1. Clamped server-side.
	NumberOfImages *int
	AspectRatio    AspectRatio
	// Seed makes generation deterministic. Imagen requires AddWatermark
	// false when Seed is set; the server enforces this.
	Seed *int64
	// AddWatermark toggles Imagen's SynthID watermark. Default: true.
	AddWatermark *bool
	// EnhancePrompt lets Imagen rewrite the prompt; the rewritten prompt
	// comes back in ImageMetadata.RevisedPrompt.
	EnhancePrompt     *bool
	SafetyFilterLevel GoogleSafetyFilterLevel
	PersonGeneration  GooglePersonGeneration
	// Language is a BCP-47 hint, e.g. "en", "ja", "de".
	Language       *string
	OutputMIMEType GoogleOutputMIMEType
	// OutputCompressionQuality is 0-100, JPEG only. Default: 75.
	OutputCompressionQuality *int
}

func (GenerateGoogle) isBackend() {}

func (b GenerateGoogle) toProto() (*painterpb.GenerateImageRequest_Google, error) {
	aspect, err := enumToProto(aspectRatioToProto, b.AspectRatio, "aspect ratio")
	if err != nil {
		return nil, err
	}

	safety, err := enumToProto(googleSafetyFilterLevelToProto, b.SafetyFilterLevel, "safety filter level")
	if err != nil {
		return nil, err
	}

	person, err := enumToProto(googlePersonGenerationToProto, b.PersonGeneration, "person generation")
	if err != nil {
		return nil, err
	}

	mime, err := enumToProto(googleOutputMIMETypeToProto, b.OutputMIMEType, "output MIME type")
	if err != nil {
		return nil, err
	}

	return &painterpb.GenerateImageRequest_Google{
		Model:                    b.Model,
		NumberOfImages:           protoconv.Int32Ptr(b.NumberOfImages),
		AspectRatio:              aspect,
		Seed:                     b.Seed,
		AddWatermark:             b.AddWatermark,
		EnhancePrompt:            b.EnhancePrompt,
		SafetyFilterLevel:        safety,
		PersonGeneration:         person,
		Language:                 b.Language,
		OutputMimeType:           mime,
		OutputCompressionQuality: protoconv.Int32Ptr(b.OutputCompressionQuality),
	}, nil
}

// GenerateOpenAI selects the OpenAI backend for Generate. Nil pointer and
// zero enum fields keep the server defaults.
type GenerateOpenAI struct {
	// Model overrides the server's SOTA model (e.g. "gpt-image-1").
	Model *string
	// N is the number of images, 1-10. Default: 1.
	N            *int
	Size         OpenAISize
	Quality      OpenAIQuality
	Background   OpenAIBackground
	OutputFormat OpenAIOutputFormat
	// OutputCompression is 0-100, JPEG/WebP only. Default: 100.
	OutputCompression *int
	Moderation        OpenAIModeration
	// User is an optional end-user identifier OpenAI uses for abuse
	// detection.
	User *string
}

func (GenerateOpenAI) isBackend() {}

func (b GenerateOpenAI) toProto() (*painterpb.GenerateImageRequest_OpenAI, error) {
	size, err := enumToProto(openAISizeToProto, b.Size, "image size")
	if err != nil {
		return nil, err
	}

	quality, err := enumToProto(openAIQualityToProto, b.Quality, "quality")
	if err != nil {
		return nil, err
	}

	background, err := enumToProto(openAIBackgroundToProto, b.Background, "background")
	if err != nil {
		return nil, err
	}

	format, err := enumToProto(openAIOutputFormatToProto, b.OutputFormat, "output format")
	if err != nil {
		return nil, err
	}

	moderation, err := enumToProto(openAIModerationToProto, b.Moderation, "moderation")
	if err != nil {
		return nil, err
	}

	return &painterpb.GenerateImageRequest_OpenAI{
		Model:             b.Model,
		N:                 protoconv.Int32Ptr(b.N),
		Size:              size,
		Quality:           quality,
		Background:        background,
		OutputFormat:      format,
		OutputCompression: protoconv.Int32Ptr(b.OutputCompression),
		Moderation:        moderation,
		User:              b.User,
	}, nil
}

// EditGoogle selects the Google Imagen capability backend for Edit. Nil
// pointer and zero enum fields keep the server defaults.
type EditGoogle struct {
	// Model overrides the server's SOTA model (e.g.
	// "imagen-3.0-capability-001").
	Model *string
	// NumberOfImages is 1-4. Default: 1.
	NumberOfImages *int
	Seed           *int64
	// GuidanceScale is the classifier-free guidance strength, roughly
	// 0-30. The server clamps.
	GuidanceScale *float32
	EditMode      GoogleEditMode
	// MaskDilation expands the masked region by that many pixels before
	// generation. Default: 0.
	MaskDilation      *int
	SafetyFilterLevel GoogleSafetyFilterLevel
	PersonGeneration  GooglePersonGeneration
	OutputMIMEType    GoogleOutputMIMEType
	// OutputCompressionQuality is 0-100, JPEG only. Default: 75.
	OutputCompressionQuality *int
}

func (EditGoogle) isBackend() {}

func (b EditGoogle) toProto() (*painterpb.EditImageRequest_Google, error) {
	editMode, err := enumToProto(googleEditModeToProto, b.EditMode, "edit mode")
	if err != nil {
		return nil, err
	}

	safety, err := enumToProto(googleSafetyFilterLevelToProto, b.SafetyFilterLevel, "safety filter level")
	if err != nil {
		return nil, err
	}

	person, err := enumToProto(googlePersonGenerationToProto, b.PersonGeneration, "person generation")
	if err != nil {
		return nil, err
	}

	mime, err := enumToProto(googleOutputMIMETypeToProto, b.OutputMIMEType, "output MIME type")
	if err != nil {
		return nil, err
	}

	return &painterpb.EditImageRequest_Google{
		Model:                    b.Model,
		NumberOfImages:           protoconv.Int32Ptr(b.NumberOfImages),
		Seed:                     b.Seed,
		GuidanceScale:            b.GuidanceScale,
		EditMode:                 editMode,
		MaskDilation:             protoconv.Int32Ptr(b.MaskDilation),
		SafetyFilterLevel:        safety,
		PersonGeneration:         person,
		OutputMimeType:           mime,
		OutputCompressionQuality: protoconv.Int32Ptr(b.OutputCompressionQuality),
	}, nil
}

// EditOpenAI selects the OpenAI backend for Edit. Nil pointer and zero
// enum fields keep the server defaults.
type EditOpenAI struct {
	// Model overrides the server's SOTA model (e.g. "gpt-image-1").
	Model *string
	// N is the number of images, 1-10. Default: 1.
	N            *int
	Size         OpenAISize
	Quality      OpenAIQuality
	Background   OpenAIBackground
	OutputFormat OpenAIOutputFormat
	// OutputCompression is 0-100, JPEG/WebP only. Default: 100.
	OutputCompression *int
	// User is an optional end-user identifier OpenAI uses for abuse
	// detection.
	User *string
}

func (EditOpenAI) isBackend() {}

func (b EditOpenAI) toProto() (*painterpb.EditImageRequest_OpenAI, error) {
	size, err := enumToProto(openAISizeToProto, b.Size, "image size")
	if err != nil {
		return nil, err
	}

	quality, err := enumToProto(openAIQualityToProto, b.Quality, "quality")
	if err != nil {
		return nil, err
	}

	background, err := enumToProto(openAIBackgroundToProto, b.Background, "background")
	if err != nil {
		return nil, err
	}

	format, err := enumToProto(openAIOutputFormatToProto, b.OutputFormat, "output format")
	if err != nil {
		return nil, err
	}

	return &painterpb.EditImageRequest_OpenAI{
		Model:             b.Model,
		N:                 protoconv.Int32Ptr(b.N),
		Size:              size,
		Quality:           quality,
		Background:        background,
		OutputFormat:      format,
		OutputCompression: protoconv.Int32Ptr(b.OutputCompression),
		User:              b.User,
	}, nil
}

// OutpaintGoogle selects the Google Imagen capability backend for
// Outpaint. The server pins the edit mode to OUTPAINT internally, so
// unlike EditGoogle there is no EditMode field. Nil pointer and zero enum
// fields keep the server defaults.
type OutpaintGoogle struct {
	Model *string
	// NumberOfImages is 1-4. Default: 1.
	NumberOfImages *int
	Seed           *int64
	// GuidanceScale is the classifier-free guidance strength, roughly
	// 0-30. The server clamps.
	GuidanceScale *float32
	// MaskDilation expands the masked region by that many pixels before
	// generation. Default: 0.
	MaskDilation      *int
	SafetyFilterLevel GoogleSafetyFilterLevel
	PersonGeneration  GooglePersonGeneration
	OutputMIMEType    GoogleOutputMIMEType
	// OutputCompressionQuality is 0-100, JPEG only. Default: 75.
	OutputCompressionQuality *int
}

func (OutpaintGoogle) isBackend() {}

func (b OutpaintGoogle) toProto() (*painterpb.OutpaintImageRequest_Google, error) {
	safety, err := enumToProto(googleSafetyFilterLevelToProto, b.SafetyFilterLevel, "safety filter level")
	if err != nil {
		return nil, err
	}

	person, err := enumToProto(googlePersonGenerationToProto, b.PersonGeneration, "person generation")
	if err != nil {
		return nil, err
	}

	mime, err := enumToProto(googleOutputMIMETypeToProto, b.OutputMIMEType, "output MIME type")
	if err != nil {
		return nil, err
	}

	return &painterpb.OutpaintImageRequest_Google{
		Model:                    b.Model,
		NumberOfImages:           protoconv.Int32Ptr(b.NumberOfImages),
		Seed:                     b.Seed,
		GuidanceScale:            b.GuidanceScale,
		MaskDilation:             protoconv.Int32Ptr(b.MaskDilation),
		SafetyFilterLevel:        safety,
		PersonGeneration:         person,
		OutputMimeType:           mime,
		OutputCompressionQuality: protoconv.Int32Ptr(b.OutputCompressionQuality),
	}, nil
}

// OutpaintOpenAI selects the OpenAI backend for Outpaint. Nil pointer and
// zero enum fields keep the server defaults.
type OutpaintOpenAI struct {
	// Model overrides the server's SOTA model (e.g. "gpt-image-1").
	Model *string
	// N is the number of images, 1-10. Default: 1.
	N            *int
	Size         OpenAISize
	Quality      OpenAIQuality
	Background   OpenAIBackground
	OutputFormat OpenAIOutputFormat
	// OutputCompression is 0-100, JPEG/WebP only. Default: 100.
	OutputCompression *int
	// User is an optional end-user identifier OpenAI uses for abuse
	// detection.
	User *string
}

func (OutpaintOpenAI) isBackend() {}

func (b OutpaintOpenAI) toProto() (*painterpb.OutpaintImageRequest_OpenAI, error) {
	size, err := enumToProto(openAISizeToProto, b.Size, "image size")
	if err != nil {
		return nil, err
	}

	quality, err := enumToProto(openAIQualityToProto, b.Quality, "quality")
	if err != nil {
		return nil, err
	}

	background, err := enumToProto(openAIBackgroundToProto, b.Background, "background")
	if err != nil {
		return nil, err
	}

	format, err := enumToProto(openAIOutputFormatToProto, b.OutputFormat, "output format")
	if err != nil {
		return nil, err
	}

	return &painterpb.OutpaintImageRequest_OpenAI{
		Model:             b.Model,
		N:                 protoconv.Int32Ptr(b.N),
		Size:              size,
		Quality:           quality,
		Background:        background,
		OutputFormat:      format,
		OutputCompression: protoconv.Int32Ptr(b.OutputCompression),
		User:              b.User,
	}, nil
}

// GenerateOptions tunes a Generate call. Nil keeps all defaults.
type GenerateOptions struct {
	// Backend must be GenerateGoogle or GenerateOpenAI (or nil for the
	// server-chosen default backend); any other variant fails locally
	// with InvalidArgument.
	Backend Backend
	// OnProgress, when set, receives progress frames as the server
	// reports them. Called synchronously from the receive loop.
	OnProgress func(Progress)
}

func (o *GenerateOptions) toProto(prompt string) (*painterpb.GenerateImageRequest_Params, error) {
	msg := &painterpb.GenerateImageRequest_Params{Prompt: prompt}

	switch b := o.Backend.(type) {
	case nil:
	case GenerateGoogle:
		google, err := b.toProto()
		if err != nil {
			return nil, err
		}

		msg.Backend = &painterpb.GenerateImageRequest_Params_Google{Google: google}
	case GenerateOpenAI:
		openai, err := b.toProto()
		if err != nil {
			return nil, err
		}

		msg.Backend = &painterpb.GenerateImageRequest_Params_Openai{Openai: openai}
	default:
		return nil, invalidArgument(fmt.Sprintf("GenerateOptions.Backend must be GenerateGoogle or GenerateOpenAI, got %T", o.Backend))
	}

	return msg, nil
}

// EditOptions tunes an Edit call. Nil keeps all defaults.
type EditOptions struct {
	// Backend must be EditGoogle or EditOpenAI (or nil for the
	// server-chosen default backend); any other variant fails locally
	// with InvalidArgument.
	Backend Backend
	// Mask optionally restricts the edit to the white regions of a mask
	// image; nil edits the whole image.
	Mask *rootpullersdk.Upload
	// OnProgress, when set, receives progress frames as the server
	// reports them. Called synchronously from the receive loop.
	OnProgress func(Progress)
}

func (o *EditOptions) toProto(prompt string) (*painterpb.EditImageRequest_Params, error) {
	msg := &painterpb.EditImageRequest_Params{Prompt: prompt}

	switch b := o.Backend.(type) {
	case nil:
	case EditGoogle:
		google, err := b.toProto()
		if err != nil {
			return nil, err
		}

		msg.Backend = &painterpb.EditImageRequest_Params_Google{Google: google}
	case EditOpenAI:
		openai, err := b.toProto()
		if err != nil {
			return nil, err
		}

		msg.Backend = &painterpb.EditImageRequest_Params_Openai{Openai: openai}
	default:
		return nil, invalidArgument(fmt.Sprintf("EditOptions.Backend must be EditGoogle or EditOpenAI, got %T", o.Backend))
	}

	return msg, nil
}

// Extend gives the number of pixels to add on each side of the input
// image during Outpaint.
type Extend struct {
	Top    int
	Bottom int
	Left   int
	Right  int
}

// OutpaintOptions tunes an Outpaint call. Nil keeps all defaults.
type OutpaintOptions struct {
	// Extend gives the number of pixels to add on each side of the input
	// image.
	Extend Extend
	// Backend must be OutpaintGoogle or OutpaintOpenAI (or nil for the
	// server-chosen default backend); any other variant fails locally
	// with InvalidArgument.
	Backend Backend
	// OnProgress, when set, receives progress frames as the server
	// reports them. Called synchronously from the receive loop.
	OnProgress func(Progress)
}

func (o *OutpaintOptions) toProto(prompt string) (*painterpb.OutpaintImageRequest_Params, error) {
	msg := &painterpb.OutpaintImageRequest_Params{
		Prompt: prompt,
		// Pixel counts clamp to the int32 range; any real extend value is
		// far below it, and the server rejects out-of-range sizes anyway.
		ExtendTop:    protoconv.ClampInt32(int64(o.Extend.Top)),
		ExtendBottom: protoconv.ClampInt32(int64(o.Extend.Bottom)),
		ExtendLeft:   protoconv.ClampInt32(int64(o.Extend.Left)),
		ExtendRight:  protoconv.ClampInt32(int64(o.Extend.Right)),
	}
	switch b := o.Backend.(type) {
	case nil:
	case OutpaintGoogle:
		google, err := b.toProto()
		if err != nil {
			return nil, err
		}

		msg.Backend = &painterpb.OutpaintImageRequest_Params_Google{Google: google}
	case OutpaintOpenAI:
		openai, err := b.toProto()
		if err != nil {
			return nil, err
		}

		msg.Backend = &painterpb.OutpaintImageRequest_Params_Openai{Openai: openai}
	default:
		return nil, invalidArgument(fmt.Sprintf("OutpaintOptions.Backend must be OutpaintGoogle or OutpaintOpenAI, got %T", o.Backend))
	}

	return msg, nil
}

// Stage identifies the generation phase a Progress frame reports. Cloud
// backends typically emit only queued / generating / uploading.
type Stage string

const (
	StageUnspecified Stage = ""
	StageQueued      Stage = "queued"
	StageGenerating  Stage = "generating"
	StageSafetyCheck Stage = "safety-check"
	StageUploading   Stage = "uploading"
)

var stageFromProto = map[painterpb.GenerationProgress_Stage]Stage{
	painterpb.GenerationProgress_STAGE_UNSPECIFIED:  StageUnspecified,
	painterpb.GenerationProgress_STAGE_QUEUED:       StageQueued,
	painterpb.GenerationProgress_STAGE_GENERATING:   StageGenerating,
	painterpb.GenerationProgress_STAGE_SAFETY_CHECK: StageSafetyCheck,
	painterpb.GenerationProgress_STAGE_UPLOADING:    StageUploading,
}

// Progress is one server progress report.
type Progress struct {
	Stage Stage
	// Percentage is the overall progress 0.0-1.0, monotonically
	// non-decreasing.
	Percentage float32
	// Message is an optional human-readable status line.
	Message string
}

func progressFromProto(p *painterpb.GenerationProgress) Progress {
	return Progress{
		Stage:      enumFromProto(stageFromProto, p.GetStage()),
		Percentage: p.GetPercentage(),
		Message:    p.GetMessage(),
	}
}

// Backend names reported in Metadata.Backend.
const (
	BackendNameGoogle = "google"
	BackendNameOpenAI = "openai"
)

var metadataBackendFromProto = map[painterpb.GenerationMetadata_Backend]string{
	painterpb.GenerationMetadata_BACKEND_UNSPECIFIED: "",
	painterpb.GenerationMetadata_BACKEND_GOOGLE:      BackendNameGoogle,
	painterpb.GenerationMetadata_BACKEND_OPENAI:      BackendNameOpenAI,
}

// ImageMetadata describes one generated image. Entries appear in
// Metadata.Images in the same order the image files are streamed; images
// suppressed by the safety filter appear here (with SafetyBlocked true)
// but produce no file in Result.Images.
type ImageMetadata struct {
	// Index is zero-based and matches the suffix on the file name
	// ("image_0.<ext>", "image_1.<ext>", ...).
	Index int
	// Seed is the seed actually used. Unset for OpenAI, which does not
	// expose used seeds.
	Seed *int64
	Size rootpullersdk.ImageSize
	// SafetyBlocked reports that the backend's safety filter suppressed
	// this image.
	SafetyBlocked bool
	SafetyReason  *string
	// RevisedPrompt is the backend-rewritten prompt when applicable
	// (Google enhance-prompt, OpenAI revised_prompt).
	RevisedPrompt *string
}

// Metadata is the call-level metadata the server streams exactly once
// per call, before any image bytes.
type Metadata struct {
	// Backend that served the call: "google", "openai", or "" when the
	// server did not report one.
	Backend string
	// Model is the model id actually used.
	Model          string
	ProcessingTime time.Duration
	NumImages      int
	Images         []ImageMetadata
	// Notes are free-form server remarks, e.g. when a requested option
	// had to be adjusted.
	Notes []string
	Usage rootpullersdk.Usage
}

func metadataFromProto(m *painterpb.GenerationMetadata) Metadata {
	images := make([]ImageMetadata, len(m.GetImages()))
	for i, im := range m.GetImages() {
		images[i] = ImageMetadata{
			Index:         int(im.GetIndex()),
			Seed:          im.Seed,
			Size:          protoconv.FromProtoImageSize(im.GetSize()),
			SafetyBlocked: im.GetSafetyBlocked(),
			SafetyReason:  im.SafetyReason,
			RevisedPrompt: im.RevisedPrompt,
		}
	}

	return Metadata{
		Backend:        enumFromProto(metadataBackendFromProto, m.GetBackend()),
		Model:          m.GetModel(),
		ProcessingTime: time.Duration(m.GetProcessingTimeMs()) * time.Millisecond,
		NumImages:      int(m.GetNumImages()),
		Images:         images,
		Notes:          m.GetNotes(),
		Usage:          protoconv.FromProtoUsage(m.GetUsage()),
	}
}

// Result is the outcome of a painter call: the generated images plus the
// call-level metadata. Images are demultiplexed by file name from the
// response chunk stream; safety-blocked images are absent here but listed
// in Metadata.Images.
type Result struct {
	Images   []rootpullersdk.File
	Metadata Metadata
}
