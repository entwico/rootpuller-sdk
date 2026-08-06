package unshakaler_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/unshakaler"
)

var errModelNotFound = errors.New("model not found")

func newService(t *testing.T, baseURL string, opts ...unshakaler.Option) *unshakaler.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return unshakaler.NewService(sdk, opts...)
}

func TestUpscaleImageRoundTrip(t *testing.T) {
	t.Parallel()

	// 5 MiB input → 3 upload chunks; 5 MiB output → 3 download chunks.
	payload := bytes.Repeat([]byte{0xAB}, 5<<20)

	var gotModel string

	srv := rootpullertest.NewServer(t, &rootpullertest.Unshakaler{
		Progress: []float32{25, 50, 100},
		UpscaleFunc: func(model string, image rootpullersdk.File) (*rootpullersdk.File, error) {
			gotModel = model

			return &rootpullersdk.File{Name: "big-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		},
	})

	svc := newService(t, srv.URL)

	var progress []float32

	result, err := svc.UpscaleImage(t.Context(),
		rootpullersdk.UploadBytes("photo.png", "image/png", payload),
		&unshakaler.Options{
			Model:       "realesrgan-x4plus",
			ScaleFactor: 4,
			Format:      rootpullersdk.ImageFormatPNG,
			OnProgress:  func(p float32) { progress = append(progress, p) },
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotModel != "realesrgan-x4plus" {
		t.Errorf("server saw model %q", gotModel)
	}

	if result.Name != "big-photo.png" || result.MIMEType != "image/png" {
		t.Errorf("unexpected result meta: %+v", result.Name)
	}

	if !bytes.Equal(result.Data, payload) {
		t.Errorf("payload mismatch: got %d bytes, want %d", len(result.Data), len(payload))
	}

	if !slices.Equal(progress, []float32{25, 50, 100}) {
		t.Errorf("progress = %v, want [25 50 100]", progress)
	}
}

func TestServiceDefaults(t *testing.T) {
	t.Parallel()

	var gotModel string

	srv := rootpullertest.NewServer(t, &rootpullertest.Unshakaler{
		UpscaleFunc: func(model string, image rootpullersdk.File) (*rootpullersdk.File, error) {
			gotModel = model

			return &rootpullersdk.File{Name: image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		},
	})

	svc := newService(t, srv.URL, unshakaler.WithDefaultModel("realesrgan-x4plus"))

	// The construction-time default model applies when Options leave it
	// empty...
	_, err := svc.UpscaleImage(t.Context(),
		rootpullersdk.UploadBytes("x.png", "image/png", []byte{1}), nil)
	if err != nil {
		t.Fatal(err)
	}

	if gotModel != "realesrgan-x4plus" {
		t.Errorf("model = %q, want service default", gotModel)
	}

	// ...and a per-call model wins over the default.
	_, err = svc.UpscaleImage(t.Context(),
		rootpullersdk.UploadBytes("x.png", "image/png", []byte{1}),
		&unshakaler.Options{Model: "other"})
	if err != nil {
		t.Fatal(err)
	}

	if gotModel != "other" {
		t.Errorf("model = %q, want per-call override", gotModel)
	}
}

func TestUpscaleImageServerError(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Unshakaler{
		UpscaleFunc: func(string, rootpullersdk.File) (*rootpullersdk.File, error) {
			return nil, errModelNotFound
		},
	})

	svc := newService(t, srv.URL)

	_, err := svc.UpscaleImage(t.Context(),
		rootpullersdk.UploadBytes("x.png", "image/png", []byte{1}),
		&unshakaler.Options{Model: "nope"},
	)
	if err == nil {
		t.Fatal("want error")
	}

	if _, ok := errors.AsType[*rootpullersdk.Error](err); !ok {
		t.Fatalf("err = %#v, want *rootpullersdk.Error", err)
	}
}

func TestUpscaleImageInvalidFormatFailsLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	_, err := svc.UpscaleImage(t.Context(),
		rootpullersdk.UploadBytes("x.png", "image/png", []byte{1}),
		&unshakaler.Options{Format: rootpullersdk.ImageFormat("bmp")},
	)
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
