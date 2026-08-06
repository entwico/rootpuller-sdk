package unshakaler_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	rootpuller "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/common"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/unshakaler"
)

var errModelNotFound = errors.New("model not found")

func TestUpscaleImageRoundTrip(t *testing.T) {
	t.Parallel()

	// 5 MiB input → 3 upload chunks; 5 MiB output → 3 download chunks.
	payload := bytes.Repeat([]byte{0xAB}, 5<<20)

	var gotModel string

	srv := rootpullertest.NewServer(t, &rootpullertest.Unshakaler{
		Progress: []float32{25, 50, 100},
		UpscaleFunc: func(model string, image common.File) (*common.File, error) {
			gotModel = model

			return &common.File{Name: "big-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		},
	})

	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	var progress []float32

	result, err := c.Unshakaler().UpscaleImage(t.Context(),
		&unshakaler.UpscaleParams{
			Model:       "realesrgan-x4plus",
			ScaleFactor: new(4),
			Format:      common.ImageFormatPNG,
			OnProgress:  func(p float32) { progress = append(progress, p) },
		},
		common.UploadBytes("photo.png", "image/png", payload),
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

func TestUpscaleImageServerError(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Unshakaler{
		UpscaleFunc: func(string, common.File) (*common.File, error) {
			return nil, errModelNotFound
		},
	})

	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Unshakaler().UpscaleImage(t.Context(),
		&unshakaler.UpscaleParams{Model: "nope"},
		common.UploadBytes("x.png", "image/png", []byte{1}),
	)
	if err == nil {
		t.Fatal("want error")
	}

	if _, ok := errors.AsType[*apierror.Error](err); !ok {
		t.Fatalf("err = %#v, want *apierror.Error", err)
	}
}

func TestUpscaleImageInvalidFormatFailsLocally(t *testing.T) {
	t.Parallel()

	c, err := rootpuller.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Unshakaler().UpscaleImage(t.Context(),
		&unshakaler.UpscaleParams{Format: common.ImageFormat("bmp")},
		common.UploadBytes("x.png", "image/png", []byte{1}),
	)
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
