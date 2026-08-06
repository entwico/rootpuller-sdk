package bgremover_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpuller "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/bgremover"
	"github.com/entwico/rootpuller-sdk/common"
	"github.com/entwico/rootpuller-sdk/internal/transport"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

// newClient dials baseURL the same way rootpuller.New does. The
// rootpuller.Client accessor for this service is wired separately, so the
// tests construct the service client directly from a transport.Core.
func newClient(t *testing.T, baseURL string) *bgremover.Client {
	t.Helper()
	httpClient, err := transport.NewHTTPClient(baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return bgremover.NewFromCore(&transport.Core{
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		ClientOpts: []connect.ClientOption{connect.WithGRPC()},
	})
}

func TestRemoveBackgroundRoundTrip(t *testing.T) {
	// 5 MiB input → 3 upload chunks; 5 MiB output → 3 download chunks.
	payload := bytes.Repeat([]byte{0xAB}, 5<<20)
	meta := &bgremover.Metadata{
		ProcessingTime:    1500 * time.Millisecond,
		Size:              common.ImageSize{Width: 800, Height: 600},
		Device:            "cuda",
		MaskConfidence:    0.93,
		ForegroundPercent: 41.5,
		BoundingBox: &common.BoundingBox{
			Position: common.Point{X: 10, Y: 20},
			Size:     common.ImageSize{Width: 300, Height: 400},
		},
	}
	var gotInput common.File
	srv := rootpullertest.NewServer(t, &rootpullertest.BgRemover{
		Metadata: meta,
		RemoveFunc: func(image common.File) (*common.File, error) {
			gotInput = image
			return &common.File{Name: "nobg-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		},
	})

	c := newClient(t, srv.URL)
	result, err := c.RemoveBackground(t.Context(),
		&bgremover.Params{
			Threshold:      rootpuller.Ptr(float32(0.4)),
			Erode:          rootpuller.Ptr(2),
			MorphologyMode: bgremover.MorphologyModeClosing,
		},
		common.UploadBytes("photo.png", "image/png", payload),
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
	if m.Size != (common.ImageSize{Width: 800, Height: 600}) {
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
	srv := rootpullertest.NewServer(t, &rootpullertest.BgRemover{
		Metadata: &bgremover.Metadata{Device: "cpu"},
	})
	c := newClient(t, srv.URL)
	result, err := c.RemoveBackground(t.Context(), &bgremover.Params{},
		common.UploadBytes("x.png", "image/png", []byte{1, 2, 3}))
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
	srv := rootpullertest.NewServer(t, &rootpullertest.BgRemover{
		RemoveFunc: func(common.File) (*common.File, error) {
			return nil, errors.New("model not loaded")
		},
	})
	c := newClient(t, srv.URL)
	_, err := c.RemoveBackground(t.Context(), &bgremover.Params{},
		common.UploadBytes("x.png", "image/png", []byte{1}))
	if err == nil {
		t.Fatal("want error")
	}
	if _, ok := errors.AsType[*apierror.Error](err); !ok {
		t.Fatalf("err = %#v, want *apierror.Error", err)
	}
}

func TestRemoveBackgroundInvalidModeFailsLocally(t *testing.T) {
	// No server: local validation must fail before any dial.
	c := newClient(t, "http://127.0.0.1:1")
	_, err := c.RemoveBackground(t.Context(),
		&bgremover.Params{MorphologyMode: bgremover.MorphologyMode("erosion")},
		common.UploadBytes("x.png", "image/png", []byte{1}),
	)
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
