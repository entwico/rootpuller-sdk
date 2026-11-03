package painter_test

import (
	"bytes"
	"errors"
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

func pngUpload(name string, data []byte) rootpullersdk.Upload {
	return rootpullersdk.UploadBytes(name, mimePNG, data)
}

func oneImage() []rootpullersdk.File {
	return []rootpullersdk.File{{Name: image0PNG, MIMEType: mimePNG, Data: []byte{0x89}}}
}

const mimePNG = "image/png"
const image0PNG = "image_0.png"
