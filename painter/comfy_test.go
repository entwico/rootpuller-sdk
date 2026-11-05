package painter_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/painter"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

func TestGenerateComfyReferencesInOrder(t *testing.T) {
	t.Parallel()

	// the second reference spans two chunks; both uploads share a name, which
	// must not merge them into one reference.
	bottle := bytes.Repeat([]byte{0x01}, 3<<20)
	label := []byte{0x02, 0x03}

	var got rootpullertest.GenerateRequest

	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{
		GenerateRequestFunc: func(req rootpullertest.GenerateRequest) ([]rootpullersdk.File, error) {
			got = req

			return oneImage(), nil
		},
	})

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	svc := painter.NewService(sdk, painter.WithComfyUIDeployment("local"))

	_, err = svc.Generate(t.Context(), "the bottle from <image1> with the label from <image2>", &painter.GenerateOptions{
		Backend: painter.GenerateComfy{Model: painter.ComfyModelQwenImage, Seed: new(int64(7))},
		References: []rootpullersdk.Upload{
			pngUpload("photo.png", bottle),
			pngUpload("photo.png", label),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Backend != painter.BackendNameComfy || got.Model != painter.ComfyModelQwenImage {
		t.Errorf("backend/model = %q/%q, want comfy/%s", got.Backend, got.Model, painter.ComfyModelQwenImage)
	}

	if len(got.References) != 2 {
		t.Fatalf("references = %d, want 2", len(got.References))
	}

	if !bytes.Equal(got.References[0].Data, bottle) || !bytes.Equal(got.References[1].Data, label) {
		t.Error("references arrived out of order or merged")
	}

	if got.ComfyUIDeployment != "local" {
		t.Errorf("deployment header = %q, want local", got.ComfyUIDeployment)
	}
}

func TestGenerateComfyContextDeploymentWins(t *testing.T) {
	t.Parallel()

	var deployment string

	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{
		GenerateRequestFunc: func(req rootpullertest.GenerateRequest) ([]rootpullersdk.File, error) {
			deployment = req.ComfyUIDeployment

			return oneImage(), nil
		},
	})

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	svc := painter.NewService(sdk, painter.WithComfyUIDeployment("local"))
	ctx := rootpullersdk.ContextWithComfyUIDeployment(t.Context(), "cloud")

	_, err = svc.Generate(ctx, "x", &painter.GenerateOptions{Backend: painter.GenerateComfy{Model: painter.ComfyModelMOSSImage}})
	if err != nil {
		t.Fatal(err)
	}

	if deployment != "cloud" {
		t.Errorf("deployment header = %q, want cloud", deployment)
	}
}

func TestComfyLocalValidation(t *testing.T) {
	t.Parallel()

	// no server: these must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")
	ref := pngUpload("a.png", []byte{1})
	mask := pngUpload("m.png", []byte{1})

	cases := map[string]func() error{
		"references without comfy": func() error {
			_, err := svc.Generate(t.Context(), "x", &painter.GenerateOptions{
				Backend: painter.GenerateOpenAI{}, References: []rootpullersdk.Upload{ref},
			})

			return err
		},
		"comfy without model": func() error {
			_, err := svc.Generate(t.Context(), "x", &painter.GenerateOptions{Backend: painter.GenerateComfy{}})

			return err
		},
		"refine without backend": func() error {
			_, err := svc.RefineFaces(t.Context(), "", ref, nil)

			return err
		},
		"refine with another backend": func() error {
			_, err := svc.RefineFaces(t.Context(), "", ref, &painter.RefineFacesOptions{
				Backend: painter.EditComfy{Model: painter.ComfyModelQwenImage},
			})

			return err
		},
		"refine without model": func() error {
			_, err := svc.RefineFaces(t.Context(), "", ref, &painter.RefineFacesOptions{Backend: painter.RefineFacesComfy{}})

			return err
		},
		"comfy edit with mask": func() error {
			_, err := svc.Edit(t.Context(), "x", ref, &painter.EditOptions{
				Backend: painter.EditComfy{Model: painter.ComfyModelQwenImage}, Mask: &mask,
			})

			return err
		},
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := call(); !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
				t.Errorf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestRefineFaces(t *testing.T) {
	t.Parallel()

	// the input spans two chunks, and two references share a file name.
	sheet := bytes.Repeat([]byte{0x05}, 3<<20)
	front, side := []byte{0x06}, []byte{0x07, 0x08}

	var got rootpullertest.RefineFacesRequest

	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{
		RefineFacesFunc: func(req rootpullertest.RefineFacesRequest) ([]rootpullersdk.File, error) {
			got = req

			return oneImage(), nil
		},
		Metadata: &painter.Metadata{
			Backend:   painter.BackendNameComfy,
			NumImages: 1,
			Images:    []painter.ImageMetadata{{Index: 0, Seed: new(int64(3))}},
			Faces:     []painter.Region{{X: 0.1, Y: 0.1, Width: 0.05, Height: 0.09}},
		},
	})

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	svc := painter.NewService(sdk, painter.WithComfyUIDeployment("local"))

	res, err := svc.RefineFaces(t.Context(), "natural skin texture", pngUpload("sheet.png", sheet), &painter.RefineFacesOptions{
		Backend:       painter.RefineFacesComfy{Model: painter.ComfyModelQwenImage, Denoise: new(0.9)},
		References:    []rootpullersdk.Upload{pngUpload("face.png", front), pngUpload("face.png", side)},
		SearchRegions: []painter.Region{{X: 0, Y: 0, Width: 1.0 / 3, Height: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Prompt != "natural skin texture" || got.Model != painter.ComfyModelQwenImage || got.ComfyUIDeployment != "local" {
		t.Errorf("request = %q/%q/%q", got.Prompt, got.Model, got.ComfyUIDeployment)
	}

	if !bytes.Equal(got.Image.Data, sheet) {
		t.Error("input image arrived incomplete")
	}

	if len(got.References) != 2 || !bytes.Equal(got.References[0].Data, front) || !bytes.Equal(got.References[1].Data, side) {
		t.Errorf("references = %d, arrived out of order or merged", len(got.References))
	}

	if len(got.SearchRegions) != 1 || got.SearchRegions[0].Width < 0.33 || got.SearchRegions[0].Width > 0.34 {
		t.Errorf("search regions = %+v", got.SearchRegions)
	}

	if len(res.Images) != 1 || len(res.Metadata.Faces) != 1 || res.Metadata.Faces[0].Height < 0.089 {
		t.Errorf("result = %d images, faces %+v", len(res.Images), res.Metadata.Faces)
	}
}

func pngUpload(name string, data []byte) rootpullersdk.Upload {
	return rootpullersdk.UploadBytes(name, mimePNG, data)
}

func oneImage() []rootpullersdk.File {
	return []rootpullersdk.File{{Name: image0PNG, MIMEType: mimePNG, Data: []byte{0x89}}}
}

const mimePNG = "image/png"
const image0PNG = "image_0.png"

func TestComfySamplingReachesTheServer(t *testing.T) {
	t.Parallel()

	var (
		gen  rootpullertest.GenerateRequest
		edit rootpullertest.EditRequest
	)

	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{
		GenerateRequestFunc: func(req rootpullertest.GenerateRequest) ([]rootpullersdk.File, error) {
			gen = req

			return oneImage(), nil
		},
		EditRequestFunc: func(req rootpullertest.EditRequest) ([]rootpullersdk.File, error) {
			edit = req

			return oneImage(), nil
		},
	})

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	svc := painter.NewService(sdk)
	negative := "text, lettering, logo"

	_, err = svc.Generate(t.Context(), "a photographer", &painter.GenerateOptions{
		Backend: painter.GenerateComfy{Model: painter.ComfyModelQwenImage, NegativePrompt: negative, GuidanceScale: new(3.1)},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3.1 is not exact in float32; the fake reports it the way the server
	// uses it.
	if gen.NegativePrompt != negative || gen.GuidanceScale == nil || *gen.GuidanceScale != 3.1 {
		t.Fatalf("generate = %q / %v", gen.NegativePrompt, gen.GuidanceScale)
	}

	_, err = svc.Edit(t.Context(), "the same person as a photo", pngUpload("sheet.png", []byte{0x89}), &painter.EditOptions{
		Backend: painter.EditComfy{Model: painter.ComfyModelQwenImage, NegativePrompt: negative, GuidanceScale: new(2.5)},
	})
	if err != nil {
		t.Fatal(err)
	}

	if edit.Backend != painter.BackendNameComfy || edit.NegativePrompt != negative || edit.GuidanceScale == nil || *edit.GuidanceScale != 2.5 {
		t.Fatalf("edit = %+v", edit)
	}
}

const negativeText = "text"

func TestComfySamplingRulesInTheFake(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{})

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	svc := painter.NewService(sdk)

	for name, backend := range map[string]painter.GenerateComfy{
		"qwen negative at its default guidance": {Model: painter.ComfyModelQwenImage, NegativePrompt: negativeText},
		"negative at guidance 1":                {Model: painter.ComfyModelMOSSImage, NegativePrompt: negativeText, GuidanceScale: new(1.0)},
		"krea takes no guidance":                {Model: painter.ComfyModelKrea2Turbo, GuidanceScale: new(3.0)},
		"guidance above 10":                     {Model: painter.ComfyModelQwenImage, GuidanceScale: new(11.0)},
		"guidance that rounds to 1 with a negative": {
			Model: painter.ComfyModelMOSSImage, NegativePrompt: negativeText, GuidanceScale: new(1.0004),
		},
		"negative over 2000 characters": {
			Model: painter.ComfyModelQwenImage, NegativePrompt: strings.Repeat("ä", 2001), GuidanceScale: new(4.0),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.Generate(t.Context(), "p", &painter.GenerateOptions{Backend: backend})
			if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}

	for name, backend := range map[string]painter.GenerateComfy{
		"moss guides at 3 by default":            {Model: painter.ComfyModelMOSSImage, NegativePrompt: negativeText},
		"guidance that rounds into range":        {Model: painter.ComfyModelQwenImage, GuidanceScale: new(10.0004)},
		"negative at exactly 2000 characters":    {Model: painter.ComfyModelQwenImage, NegativePrompt: strings.Repeat("ä", 2000), GuidanceScale: new(4.0)},
		"a model the fake does not know is left": {Model: "some-new-model", NegativePrompt: negativeText},
	} {
		if _, err := svc.Generate(t.Context(), "p", &painter.GenerateOptions{Backend: backend}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
