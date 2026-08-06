package facefixer_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/common"
	"github.com/entwico/rootpuller-sdk/facefixer"
	"github.com/entwico/rootpuller-sdk/internal/transport"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

var errNoFaceDetected = errors.New("no face detected")

// newClient dials baseURL the same way rootpuller.New does. The
// rootpuller.Client accessor for this service is wired separately, so the
// tests construct the service client directly from a transport.Core.
func newClient(t *testing.T, baseURL string) *facefixer.Client {
	t.Helper()

	httpClient, err := transport.NewHTTPClient(baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	return facefixer.NewFromCore(&transport.Core{
		HTTPClient: httpClient,
		BaseURL:    baseURL,
		ClientOpts: []connect.ClientOption{connect.WithGRPC()},
	})
}

func TestRestoreFaceRoundTrip(t *testing.T) {
	t.Parallel()

	// 5 MiB input → 3 upload chunks; 5 MiB output → 3 download chunks.
	payload := bytes.Repeat([]byte{0xCD}, 5<<20)
	meta := &facefixer.Metadata{
		FacesDetected:     2,
		UpscaleFactor:     4,
		FidelityWeight:    0.7,
		OnlyCenterFace:    false,
		EnhanceBackground: true,
		ProcessingTime:    2300 * time.Millisecond,
		InputSize:         common.ImageSize{Width: 512, Height: 512},
		OutputSize:        common.ImageSize{Width: 2048, Height: 2048},
		Model:             "codeformer",
		Operation:         "restore",
		Faces: []facefixer.FaceInfo{
			{
				OriginalBBox: common.BoundingBox{
					Position: common.Point{X: 10, Y: 20},
					Size:     common.ImageSize{Width: 100, Height: 120},
				},
				Confidence: 0.98,
				Landmarks: &facefixer.Landmarks{
					LeftEye:    common.Point{X: 30, Y: 50},
					RightEye:   common.Point{X: 70, Y: 50},
					Nose:       common.Point{X: 50, Y: 70},
					LeftMouth:  common.Point{X: 35, Y: 95},
					RightMouth: common.Point{X: 65, Y: 95},
				},
				RestoredBBox: &common.BoundingBox{
					Position: common.Point{X: 40, Y: 80},
					Size:     common.ImageSize{Width: 400, Height: 480},
				},
			},
			{
				OriginalBBox: common.BoundingBox{
					Position: common.Point{X: 200, Y: 30},
					Size:     common.ImageSize{Width: 90, Height: 110},
				},
				Confidence: 0.55,
				// Landmarks and RestoredBBox absent: must map to nil.
			},
		},
	}

	var (
		gotOp   string
		gotMask *common.File
	)

	srv := rootpullertest.NewServer(t, &rootpullertest.FaceFixer{
		Metadata: meta,
		FixFunc: func(op string, image common.File, mask *common.File) (*common.File, error) {
			gotOp = op
			gotMask = mask

			return &common.File{Name: op + "-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		},
	})

	c := newClient(t, srv.URL)

	result, err := c.RestoreFace(t.Context(),
		&facefixer.RestoreParams{
			Upscale:        new(4),
			FidelityWeight: new(float32(0.7)),
			HasAligned:     new(false),
			Format:         common.ImageFormatPNG,
		},
		common.UploadBytes("portrait.png", "image/png", payload),
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotOp != "restore" {
		t.Errorf("server saw op %q, want restore", gotOp)
	}

	if gotMask != nil {
		t.Errorf("server saw mask %+v, want nil for RestoreFace", gotMask)
	}

	if result.File.Name != "restore-portrait.png" || !bytes.Equal(result.File.Data, payload) {
		t.Errorf("unexpected result file: %q, %d bytes", result.File.Name, len(result.File.Data))
	}

	m := result.Metadata
	if m.FacesDetected != 2 || m.UpscaleFactor != 4 || m.FidelityWeight != 0.7 {
		t.Errorf("unexpected metadata scalars: %+v", m)
	}

	if m.OnlyCenterFace || !m.EnhanceBackground {
		t.Errorf("unexpected metadata flags: %+v", m)
	}

	if m.ProcessingTime != 2300*time.Millisecond {
		t.Errorf("ProcessingTime = %v, want 2.3s", m.ProcessingTime)
	}

	if m.InputSize != (common.ImageSize{Width: 512, Height: 512}) || m.OutputSize != (common.ImageSize{Width: 2048, Height: 2048}) {
		t.Errorf("sizes = %+v / %+v", m.InputSize, m.OutputSize)
	}

	if m.Model != "codeformer" || m.Operation != "restore" {
		t.Errorf("Model/Operation = %q/%q", m.Model, m.Operation)
	}

	if m.MaskProvided != nil {
		t.Errorf("MaskProvided = %v, want nil for absent proto optional", *m.MaskProvided)
	}

	if len(m.Faces) != 2 {
		t.Fatalf("got %d faces, want 2", len(m.Faces))
	}

	f0 := m.Faces[0]
	if f0.OriginalBBox != meta.Faces[0].OriginalBBox || f0.Confidence != 0.98 {
		t.Errorf("faces[0] = %+v", f0)
	}

	if f0.Landmarks == nil || *f0.Landmarks != *meta.Faces[0].Landmarks {
		t.Errorf("faces[0].Landmarks = %+v, want %+v", f0.Landmarks, meta.Faces[0].Landmarks)
	}

	if f0.RestoredBBox == nil || *f0.RestoredBBox != *meta.Faces[0].RestoredBBox {
		t.Errorf("faces[0].RestoredBBox = %+v, want %+v", f0.RestoredBBox, meta.Faces[0].RestoredBBox)
	}

	f1 := m.Faces[1]
	if f1.Landmarks != nil || f1.RestoredBBox != nil {
		t.Errorf("faces[1] optionals = %+v / %+v, want nil / nil", f1.Landmarks, f1.RestoredBBox)
	}

	if f1.OriginalBBox != meta.Faces[1].OriginalBBox || f1.Confidence != 0.55 {
		t.Errorf("faces[1] = %+v", f1)
	}
}

func TestColorizeFaceRoundTrip(t *testing.T) {
	t.Parallel()

	var gotOp string

	srv := rootpullertest.NewServer(t, &rootpullertest.FaceFixer{
		Metadata: &facefixer.Metadata{Operation: "colorize", Model: "codeformer"},
		FixFunc: func(op string, image common.File, _ *common.File) (*common.File, error) {
			gotOp = op

			return &common.File{Name: op + "-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		},
	})
	c := newClient(t, srv.URL)

	result, err := c.ColorizeFace(t.Context(),
		&facefixer.ColorizeParams{Upscale: new(2)},
		common.UploadBytes("gray.png", "image/png", []byte{1, 2, 3}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotOp != "colorize" {
		t.Errorf("server saw op %q, want colorize", gotOp)
	}

	if result.File.Name != "colorize-gray.png" {
		t.Errorf("result file = %q", result.File.Name)
	}

	if result.Metadata.Operation != "colorize" {
		t.Errorf("Operation = %q, want colorize", result.Metadata.Operation)
	}
}

func TestInpaintFaceMaskDelivery(t *testing.T) {
	t.Parallel()

	// 5 MiB image and 3 MiB mask: both cross the 2 MiB chunk boundary.
	imagePayload := bytes.Repeat([]byte{0x11}, 5<<20)
	maskPayload := bytes.Repeat([]byte{0xFF}, 3<<20)

	var (
		gotOp    string
		gotImage common.File
		gotMask  *common.File
	)

	srv := rootpullertest.NewServer(t, &rootpullertest.FaceFixer{
		Metadata: &facefixer.Metadata{Operation: "inpaint", MaskProvided: new(true)},
		FixFunc: func(op string, image common.File, mask *common.File) (*common.File, error) {
			gotOp = op
			gotImage = image
			gotMask = mask

			return &common.File{Name: op + "-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		},
	})
	c := newClient(t, srv.URL)

	result, err := c.InpaintFace(t.Context(),
		&facefixer.InpaintParams{FidelityWeight: new(float32(0.5))},
		common.UploadBytes("damaged.png", "image/png", imagePayload),
		common.UploadBytes("mask.png", "image/png", maskPayload),
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotOp != "inpaint" {
		t.Errorf("server saw op %q, want inpaint", gotOp)
	}

	if gotImage.Name != "damaged.png" || !bytes.Equal(gotImage.Data, imagePayload) {
		t.Errorf("server saw image %q with %d bytes", gotImage.Name, len(gotImage.Data))
	}

	if gotMask == nil {
		t.Fatal("server saw no mask, want mask.png")
	}

	if gotMask.Name != "mask.png" || !bytes.Equal(gotMask.Data, maskPayload) {
		t.Errorf("server saw mask %q with %d bytes, want mask.png with %d", gotMask.Name, len(gotMask.Data), len(maskPayload))
	}

	if !bytes.Equal(result.File.Data, imagePayload) {
		t.Errorf("result payload mismatch: got %d bytes", len(result.File.Data))
	}

	if result.Metadata.MaskProvided == nil || !*result.Metadata.MaskProvided {
		t.Errorf("MaskProvided = %v, want true", result.Metadata.MaskProvided)
	}
}

func TestRestoreFaceServerError(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.FaceFixer{
		FixFunc: func(string, common.File, *common.File) (*common.File, error) {
			return nil, errNoFaceDetected
		},
	})
	c := newClient(t, srv.URL)

	_, err := c.RestoreFace(t.Context(), &facefixer.RestoreParams{},
		common.UploadBytes("x.png", "image/png", []byte{1}))
	if err == nil {
		t.Fatal("want error")
	}

	if _, ok := errors.AsType[*apierror.Error](err); !ok {
		t.Fatalf("err = %#v, want *apierror.Error", err)
	}
}

func TestInvalidFormatFailsLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	c := newClient(t, "http://127.0.0.1:1")
	upload := func() common.Upload { return common.UploadBytes("x.png", "image/png", []byte{1}) }

	_, err := c.RestoreFace(t.Context(), &facefixer.RestoreParams{Format: common.ImageFormat("bmp")}, upload())
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("RestoreFace err = %v, want ErrInvalidArgument", err)
	}

	_, err = c.ColorizeFace(t.Context(), &facefixer.ColorizeParams{Format: common.ImageFormat("bmp")}, upload())
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("ColorizeFace err = %v, want ErrInvalidArgument", err)
	}

	_, err = c.InpaintFace(t.Context(), &facefixer.InpaintParams{Format: common.ImageFormat("bmp")}, upload(), upload())
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("InpaintFace err = %v, want ErrInvalidArgument", err)
	}
}
