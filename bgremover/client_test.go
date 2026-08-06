package bgremover_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/bgremover"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

var errModelNotLoaded = errors.New("model not loaded")

func newService(t *testing.T, baseURL string) *bgremover.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return bgremover.NewService(sdk)
}

func TestRemoveBackgroundRoundTrip(t *testing.T) {
	t.Parallel()

	// 5 MiB input → 3 upload chunks; 5 MiB output → 3 download chunks.
	payload := bytes.Repeat([]byte{0xAB}, 5<<20)
	meta := &bgremover.Metadata{
		ProcessingTime:    1500 * time.Millisecond,
		Size:              rootpullersdk.ImageSize{Width: 800, Height: 600},
		Device:            "cuda",
		MaskConfidence:    0.93,
		ForegroundPercent: 41.5,
		BoundingBox: &rootpullersdk.BoundingBox{
			Position: rootpullersdk.Point{X: 10, Y: 20},
			Size:     rootpullersdk.ImageSize{Width: 300, Height: 400},
		},
	}

	var gotInput rootpullersdk.File

	srv := rootpullertest.NewServer(t, &rootpullertest.BgRemover{
		Metadata: meta,
		RemoveFunc: func(image rootpullersdk.File) (*rootpullersdk.File, error) {
			gotInput = image

			return &rootpullersdk.File{Name: "nobg-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		},
	})

	svc := newService(t, srv.URL)

	result, err := svc.RemoveBackground(t.Context(),
		rootpullersdk.UploadBytes("photo.png", "image/png", payload),
		&bgremover.Options{
			Threshold:      new(float32(0.4)),
			Erode:          2,
			MorphologyMode: bgremover.MorphologyModeClosing,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotInput.Name != "photo.png" || !bytes.Equal(gotInput.Data, payload) {
		t.Errorf("server saw %q with %d bytes, want photo.png with %d", gotInput.Name, len(gotInput.Data), len(payload))
	}

	if result.File.Name != "nobg-photo.png" || result.File.MIMEType != "image/png" {
		t.Errorf("unexpected result meta: %q %q", result.File.Name, result.File.MIMEType)
	}

	if !bytes.Equal(result.File.Data, payload) {
		t.Errorf("payload mismatch: got %d bytes, want %d", len(result.File.Data), len(payload))
	}

	m := result.Metadata
	if m.ProcessingTime != 1500*time.Millisecond {
		t.Errorf("ProcessingTime = %v, want 1.5s", m.ProcessingTime)
	}

	if m.Size != (rootpullersdk.ImageSize{Width: 800, Height: 600}) {
		t.Errorf("Size = %+v", m.Size)
	}

	if m.Device != "cuda" || m.MaskConfidence != 0.93 || m.ForegroundPercent != 41.5 {
		t.Errorf("unexpected metadata: %+v", m)
	}

	if m.BoundingBox == nil {
		t.Fatal("BoundingBox = nil, want set")
	}

	if *m.BoundingBox != *meta.BoundingBox {
		t.Errorf("BoundingBox = %+v, want %+v", *m.BoundingBox, *meta.BoundingBox)
	}
}

func TestRemoveBackgroundNilBoundingBox(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.BgRemover{
		Metadata: &bgremover.Metadata{Device: "cpu"},
	})
	svc := newService(t, srv.URL)

	result, err := svc.RemoveBackground(t.Context(),
		rootpullersdk.UploadBytes("x.png", "image/png", []byte{1, 2, 3}), nil)
	if err != nil {
		t.Fatal(err)
	}

	if result.Metadata.Device != "cpu" {
		t.Errorf("Device = %q, want cpu", result.Metadata.Device)
	}

	if result.Metadata.BoundingBox != nil {
		t.Errorf("BoundingBox = %+v, want nil for absent proto optional", result.Metadata.BoundingBox)
	}
}

func TestRemoveBackgroundServerError(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.BgRemover{
		RemoveFunc: func(rootpullersdk.File) (*rootpullersdk.File, error) {
			return nil, errModelNotLoaded
		},
	})
	svc := newService(t, srv.URL)

	_, err := svc.RemoveBackground(t.Context(),
		rootpullersdk.UploadBytes("x.png", "image/png", []byte{1}), nil)
	if err == nil {
		t.Fatal("want error")
	}

	if _, ok := errors.AsType[*rootpullersdk.Error](err); !ok {
		t.Fatalf("err = %#v, want *rootpullersdk.Error", err)
	}
}

func TestRemoveBackgroundInvalidModeFailsLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	_, err := svc.RemoveBackground(t.Context(),
		rootpullersdk.UploadBytes("x.png", "image/png", []byte{1}),
		&bgremover.Options{MorphologyMode: bgremover.MorphologyMode("erosion")},
	)
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
