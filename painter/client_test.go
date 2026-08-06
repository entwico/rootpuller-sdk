package painter_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpuller "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/common"
	"github.com/entwico/rootpuller-sdk/internal/transport"
	"github.com/entwico/rootpuller-sdk/painter"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

// newClient dials baseURL the same way rootpuller.New does. The
// rootpuller.Client accessor for this service is wired separately, so the
// tests construct the service client directly from a transport.Core.
func newClient(t *testing.T, baseURL string) *painter.Client {
	t.Helper()
	httpClient, err := transport.NewHTTPClient(baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return painter.NewFromCore(&transport.Core{
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		ClientOpts: []connect.ClientOption{connect.WithGRPC()},
	})
}

func TestGenerateMultiImageDemux(t *testing.T) {
	// Two output images over one response arm: the client must demux the
	// chunk stream by file name. 3 MiB each → 2 chunks per file.
	payload0 := bytes.Repeat([]byte{0x01}, 3<<20)
	payload1 := bytes.Repeat([]byte{0x02}, 3<<20)
	var gotPrompt string
	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{
		GenerateFunc: func(prompt string) ([]common.File, error) {
			gotPrompt = prompt
			return []common.File{
				{Name: "image_0.png", MIMEType: "image/png", Data: payload0},
				{Name: "image_1.png", MIMEType: "image/png", Data: payload1},
			}, nil
		},
		Metadata: &painter.Metadata{
			Backend:        "google",
			Model:          "imagen-3.0-generate-002",
			ProcessingTime: 1500 * time.Millisecond,
			NumImages:      2,
			Images: []painter.ImageMetadata{
				{Index: 0, Seed: rootpuller.Ptr(int64(42)), Size: common.ImageSize{Width: 1024, Height: 1024}},
				{Index: 1, Seed: rootpuller.Ptr(int64(43)), RevisedPrompt: rootpuller.Ptr("a very detailed cat")},
			},
			Notes: []string{"aspect ratio adjusted"},
			Usage: common.Usage{EstimatedCostMicros: 40000, CurrencyCode: "USD"},
		},
	})

	c := newClient(t, srv.URL)
	result, err := c.Generate(t.Context(), &painter.GenerateRequest{
		Prompt: "a cat",
		Backend: painter.GenerateGoogle{
			NumberOfImages: rootpuller.Ptr(2),
			AspectRatio:    painter.AspectRatio1x1,
			Seed:           rootpuller.Ptr(int64(42)),
			AddWatermark:   rootpuller.Ptr(false),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPrompt != "a cat" {
		t.Errorf("server saw prompt %q", gotPrompt)
	}
	if len(result.Images) != 2 {
		t.Fatalf("got %d images, want 2", len(result.Images))
	}
	if result.Images[0].Name != "image_0.png" || !bytes.Equal(result.Images[0].Data, payload0) {
		t.Errorf("image 0 mismatch: name %q, %d bytes", result.Images[0].Name, len(result.Images[0].Data))
	}
	if result.Images[1].Name != "image_1.png" || !bytes.Equal(result.Images[1].Data, payload1) {
		t.Errorf("image 1 mismatch: name %q, %d bytes", result.Images[1].Name, len(result.Images[1].Data))
	}
	meta := result.Metadata
	if meta.Backend != "google" || meta.Model != "imagen-3.0-generate-002" {
		t.Errorf("metadata backend/model = %q/%q", meta.Backend, meta.Model)
	}
	if meta.ProcessingTime != 1500*time.Millisecond || meta.NumImages != 2 {
		t.Errorf("metadata timing = %v, num = %d", meta.ProcessingTime, meta.NumImages)
	}
	if len(meta.Images) != 2 {
		t.Fatalf("got %d image metadata entries, want 2", len(meta.Images))
	}
	if meta.Images[0].Index != 0 || meta.Images[0].Seed == nil || *meta.Images[0].Seed != 42 {
		t.Errorf("images[0] = %+v", meta.Images[0])
	}
	if meta.Images[0].Size != (common.ImageSize{Width: 1024, Height: 1024}) {
		t.Errorf("images[0].Size = %+v", meta.Images[0].Size)
	}
	if meta.Images[1].Index != 1 || meta.Images[1].RevisedPrompt == nil || *meta.Images[1].RevisedPrompt != "a very detailed cat" {
		t.Errorf("images[1] = %+v", meta.Images[1])
	}
	if !slices.Equal(meta.Notes, []string{"aspect ratio adjusted"}) {
		t.Errorf("notes = %v", meta.Notes)
	}
	if meta.Usage.EstimatedCostMicros != 40000 || meta.Usage.CurrencyCode != "USD" {
		t.Errorf("usage = %+v", meta.Usage)
	}
}

func TestEditWithMaskOrdering(t *testing.T) {
	// 5 MiB image and 3 MiB mask each span multiple chunks. The fake
	// rejects any input_image chunk after a mask_image chunk, so success
	// proves the client sends params, then the full image, then the mask.
	imageData := bytes.Repeat([]byte{0xAA}, 5<<20)
	maskData := bytes.Repeat([]byte{0xBB}, 3<<20)
	var gotPrompt string
	var gotImage common.File
	var gotMask *common.File
	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{
		EditFunc: func(prompt string, image common.File, mask *common.File) ([]common.File, error) {
			gotPrompt = prompt
			gotImage = image
			gotMask = mask
			return []common.File{{Name: "image_0.png", MIMEType: "image/png", Data: []byte{0x01, 0x02}}}, nil
		},
	})

	c := newClient(t, srv.URL)
	result, err := c.Edit(t.Context(),
		&painter.EditRequest{
			Prompt: "remove the lamp post",
			Backend: painter.EditGoogle{
				EditMode:     painter.GoogleEditModeInpaintRemoval,
				MaskDilation: rootpuller.Ptr(8),
			},
		},
		common.UploadBytes("photo.png", "image/png", imageData),
		rootpuller.Ptr(common.UploadBytes("mask.png", "image/png", maskData)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPrompt != "remove the lamp post" {
		t.Errorf("server saw prompt %q", gotPrompt)
	}
	if gotImage.Name != "photo.png" || !bytes.Equal(gotImage.Data, imageData) {
		t.Errorf("server saw image %q with %d bytes", gotImage.Name, len(gotImage.Data))
	}
	if gotMask == nil {
		t.Fatal("server saw no mask")
	}
	if gotMask.Name != "mask.png" || !bytes.Equal(gotMask.Data, maskData) {
		t.Errorf("server saw mask %q with %d bytes", gotMask.Name, len(gotMask.Data))
	}
	if len(result.Images) != 1 || !bytes.Equal(result.Images[0].Data, []byte{0x01, 0x02}) {
		t.Errorf("unexpected result images: %+v", result.Images)
	}
}

func TestOutpaintRoundTrip(t *testing.T) {
	var gotPrompt string
	var gotImage common.File
	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{
		OutpaintFunc: func(prompt string, image common.File) ([]common.File, error) {
			gotPrompt = prompt
			gotImage = image
			return []common.File{{Name: "image_0.png", MIMEType: "image/png", Data: []byte{0x99}}}, nil
		},
	})

	c := newClient(t, srv.URL)
	result, err := c.Outpaint(t.Context(),
		&painter.OutpaintRequest{
			Prompt:       "extend the beach",
			ExtendTop:    64,
			ExtendBottom: 64,
			ExtendLeft:   128,
			ExtendRight:  128,
			Backend:      painter.OutpaintOpenAI{Quality: painter.OpenAIQualityHigh},
		},
		common.UploadBytes("beach.png", "image/png", []byte{0x10, 0x20, 0x30}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPrompt != "extend the beach" {
		t.Errorf("server saw prompt %q", gotPrompt)
	}
	if gotImage.Name != "beach.png" || !bytes.Equal(gotImage.Data, []byte{0x10, 0x20, 0x30}) {
		t.Errorf("server saw image %q with %d bytes", gotImage.Name, len(gotImage.Data))
	}
	if len(result.Images) != 1 || result.Images[0].Name != "image_0.png" {
		t.Fatalf("unexpected result images: %+v", result.Images)
	}
	if !bytes.Equal(result.Images[0].Data, []byte{0x99}) {
		t.Errorf("payload mismatch: %v", result.Images[0].Data)
	}
}

func TestGenerateProgressCallback(t *testing.T) {
	want := []painter.Progress{
		{Stage: painter.StageQueued, Percentage: 0},
		{Stage: painter.StageGenerating, Percentage: 0.5, Message: "rendering"},
		{Stage: painter.StageUploading, Percentage: 1},
	}
	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{Progress: want})

	c := newClient(t, srv.URL)
	var got []painter.Progress
	_, err := c.Generate(t.Context(), &painter.GenerateRequest{
		Prompt:     "a dog",
		Backend:    painter.GenerateOpenAI{N: rootpuller.Ptr(1)},
		OnProgress: func(p painter.Progress) { got = append(got, p) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("progress = %+v, want %+v", got, want)
	}
}

func TestGenerateWrongBackendVariantFailsLocally(t *testing.T) {
	// No server: the wrong Backend variant must fail before any dial.
	c := newClient(t, "http://127.0.0.1:1")
	_, err := c.Generate(t.Context(), &painter.GenerateRequest{
		Prompt:  "a cat",
		Backend: painter.EditGoogle{},
	})
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestGenerateUnknownEnumFailsLocally(t *testing.T) {
	c := newClient(t, "http://127.0.0.1:1")
	_, err := c.Generate(t.Context(), &painter.GenerateRequest{
		Prompt:  "a cat",
		Backend: painter.GenerateGoogle{AspectRatio: painter.AspectRatio("21:9")},
	})
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestGenerateServerError(t *testing.T) {
	srv := rootpullertest.NewServer(t, &rootpullertest.Painter{
		GenerateFunc: func(string) ([]common.File, error) {
			return nil, errors.New("backend unavailable")
		},
	})
	c := newClient(t, srv.URL)
	_, err := c.Generate(t.Context(), &painter.GenerateRequest{Prompt: "a cat"})
	if err == nil {
		t.Fatal("want error")
	}
	if _, ok := errors.AsType[*apierror.Error](err); !ok {
		t.Fatalf("err = %#v, want *apierror.Error", err)
	}
}
