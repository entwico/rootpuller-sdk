package assetia_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/assetia"
	assetiapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/assetia"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/assetia/assetiaconnect"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

var errTransformFailed = errors.New("transform failed")

func newService(t *testing.T, baseURL string) *assetia.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return assetia.NewService(sdk)
}

func TestTransformImageRoundTrip(t *testing.T) {
	t.Parallel()

	// 5 MiB image → 3 upload chunks; 3 MiB watermark → 2 chunks.
	payload := bytes.Repeat([]byte{0xAB}, 5<<20)
	watermarkPayload := bytes.Repeat([]byte{0x7F}, 3<<20)

	meta := &rootpullertest.AssetiaImageMetadata{
		Original: assetia.ImageMetadata{
			Size:            rootpullersdk.ImageSize{Width: 4000, Height: 3000},
			Format:          rootpullersdk.ImageFormatJPG,
			ColorSpace:      "srgb",
			HasICCProfile:   true,
			Channels:        3,
			EXIFOrientation: 6,
			Extra:           map[string]any{"make": "Canon", "iso": float64(200)},
		},
		Result: assetia.ImageMetadata{
			Size:       rootpullersdk.ImageSize{Width: 800, Height: 600},
			Format:     rootpullersdk.ImageFormatWebP,
			ColorSpace: "srgb",
			HasAlpha:   true,
			Channels:   4,
		},
		ProcessingTime: 1234 * time.Millisecond,
	}

	var (
		gotImage     rootpullersdk.File
		gotWatermark *rootpullersdk.File
	)

	srv := rootpullertest.NewServer(t, &rootpullertest.Assetia{
		ImageMetadata: meta,
		TransformImageFunc: func(image rootpullersdk.File, watermark *rootpullersdk.File) (*rootpullersdk.File, error) {
			gotImage = image
			gotWatermark = watermark

			return &rootpullersdk.File{Name: "transformed-" + image.Name, MIMEType: "image/webp", Data: image.Data}, nil
		},
	})
	svc := newService(t, srv.URL)

	result, err := svc.TransformImage(t.Context(),
		rootpullersdk.UploadBytes("photo.jpg", "image/jpeg", payload),
		&assetia.TransformImageOptions{
			Watermark: &assetia.Watermark{
				Image:   rootpullersdk.UploadBytes("wm.png", "image/png", watermarkPayload),
				Opacity: 50,
			},
			Output: &assetia.Output{Format: rootpullersdk.ImageFormatWebP},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// The image and the watermark must be reassembled separately.
	if gotImage.Name != "photo.jpg" || !bytes.Equal(gotImage.Data, payload) {
		t.Errorf("server saw image %q with %d bytes", gotImage.Name, len(gotImage.Data))
	}

	if gotWatermark == nil {
		t.Fatal("server saw no watermark, want wm.png")
	}

	if gotWatermark.Name != "wm.png" || !bytes.Equal(gotWatermark.Data, watermarkPayload) {
		t.Errorf("server saw watermark %q with %d bytes, want wm.png with %d", gotWatermark.Name, len(gotWatermark.Data), len(watermarkPayload))
	}

	if result.File.Name != "transformed-photo.jpg" || !bytes.Equal(result.File.Data, payload) {
		t.Errorf("unexpected result file: %q, %d bytes", result.File.Name, len(result.File.Data))
	}

	if result.ProcessingTime != 1234*time.Millisecond {
		t.Errorf("ProcessingTime = %v, want 1.234s", result.ProcessingTime)
	}

	o := result.Original
	if o.Size != meta.Original.Size || o.Format != rootpullersdk.ImageFormatJPG || o.ColorSpace != "srgb" {
		t.Errorf("unexpected original metadata: %+v", o)
	}

	if o.HasAlpha || !o.HasICCProfile || o.Channels != 3 || o.EXIFOrientation != 6 {
		t.Errorf("unexpected original flags: %+v", o)
	}

	if o.Extra["make"] != "Canon" || o.Extra["iso"] != float64(200) {
		t.Errorf("original Extra = %v", o.Extra)
	}

	r := result.Result
	if r.Size != meta.Result.Size || r.Format != rootpullersdk.ImageFormatWebP || !r.HasAlpha || r.Channels != 4 {
		t.Errorf("unexpected result metadata: %+v", r)
	}

	if r.Extra != nil {
		t.Errorf("result Extra = %v, want nil for absent proto optional", r.Extra)
	}
}

// capturingMedia records the params frame of each bidi call and answers
// with a minimal metadata frame and one file chunk.
type capturingMedia struct {
	assetiaconnect.UnimplementedMediaProcessingServiceHandler

	imageParams   chan *assetiapb.TransformImageRequest_Params
	videoParams   chan *assetiapb.TransformVideoRequest_Params
	previewParams chan *assetiapb.GeneratePreviewRequest_Params
}

func drainParams[Req, Resp, Params any](stream *connect.BidiStream[Req, Resp], params func(*Req) (Params, bool), out chan Params) error {
	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return err
		}

		if p, ok := params(req); ok {
			out <- p
		}
	}
}

func (h *capturingMedia) TransformImage(_ context.Context, stream *connect.BidiStream[assetiapb.TransformImageRequest, assetiapb.TransformImageResponse]) error {
	err := drainParams(stream, func(req *assetiapb.TransformImageRequest) (*assetiapb.TransformImageRequest_Params, bool) {
		p, ok := req.GetRequest().(*assetiapb.TransformImageRequest_Params_)
		if !ok {
			return nil, false
		}

		return p.Params, true
	}, h.imageParams)
	if err != nil {
		return err
	}

	if err := stream.Send(&assetiapb.TransformImageResponse{
		Response: &assetiapb.TransformImageResponse_Metadata{Metadata: &assetiapb.TransformImageMetadata{}},
	}); err != nil {
		return err
	}

	return stream.Send(&assetiapb.TransformImageResponse{
		Response: &assetiapb.TransformImageResponse_File{File: &commonpb.FileChunk{Name: "out.bin", Data: []byte{1}, ContentLength: 1}},
	})
}

func (h *capturingMedia) TransformVideo(_ context.Context, stream *connect.BidiStream[assetiapb.TransformVideoRequest, assetiapb.TransformVideoResponse]) error {
	err := drainParams(stream, func(req *assetiapb.TransformVideoRequest) (*assetiapb.TransformVideoRequest_Params, bool) {
		p, ok := req.GetRequest().(*assetiapb.TransformVideoRequest_Params_)
		if !ok {
			return nil, false
		}

		return p.Params, true
	}, h.videoParams)
	if err != nil {
		return err
	}

	if err := stream.Send(&assetiapb.TransformVideoResponse{
		Response: &assetiapb.TransformVideoResponse_Metadata{Metadata: &assetiapb.TransformVideoMetadata{}},
	}); err != nil {
		return err
	}

	return stream.Send(&assetiapb.TransformVideoResponse{
		Response: &assetiapb.TransformVideoResponse_File{File: &commonpb.FileChunk{Name: "out.bin", Data: []byte{1}, ContentLength: 1}},
	})
}

func (h *capturingMedia) GeneratePreview(_ context.Context, stream *connect.BidiStream[assetiapb.GeneratePreviewRequest, assetiapb.GeneratePreviewResponse]) error {
	err := drainParams(stream, func(req *assetiapb.GeneratePreviewRequest) (*assetiapb.GeneratePreviewRequest_Params, bool) {
		p, ok := req.GetRequest().(*assetiapb.GeneratePreviewRequest_Params_)
		if !ok {
			return nil, false
		}

		return p.Params, true
	}, h.previewParams)
	if err != nil {
		return err
	}

	if err := stream.Send(&assetiapb.GeneratePreviewResponse{
		Response: &assetiapb.GeneratePreviewResponse_Metadata{Metadata: &assetiapb.GeneratePreviewMetadata{}},
	}); err != nil {
		return err
	}

	return stream.Send(&assetiapb.GeneratePreviewResponse{
		Response: &assetiapb.GeneratePreviewResponse_File{File: &commonpb.FileChunk{Name: "out.bin", Data: []byte{1}, ContentLength: 1}},
	})
}

func newCapturingServer(t *testing.T) (*rootpullertest.Server, *capturingMedia) {
	t.Helper()

	handler := &capturingMedia{
		imageParams:   make(chan *assetiapb.TransformImageRequest_Params, 1),
		videoParams:   make(chan *assetiapb.TransformVideoRequest_Params, 1),
		previewParams: make(chan *assetiapb.GeneratePreviewRequest_Params, 1),
	}
	mux := http.NewServeMux()
	mux.Handle(assetiaconnect.NewMediaProcessingServiceHandler(handler))

	return rootpullertest.NewServerWithMux(t, mux), handler
}

func upload() rootpullersdk.Upload {
	return rootpullersdk.UploadBytes("x.bin", "application/octet-stream", []byte{1})
}

func TestTransformImageOptionsRoundTrip(t *testing.T) {
	t.Parallel()

	srv, handler := newCapturingServer(t)
	svc := newService(t, srv.URL)

	opts := &assetia.TransformImageOptions{
		Crop:   &assetia.Crop{Left: 10, Top: 20, Width: 300, Height: 400},
		Rotate: 90,
		Resize: &assetia.Resize{Width: 800, Height: 600},
		Watermark: &assetia.Watermark{
			Image:   rootpullersdk.UploadBytes("wm.png", "image/png", []byte{2}),
			Opacity: 50,
		},
		Output: &assetia.Output{
			Format:           rootpullersdk.ImageFormatWebP,
			Quality:          80,
			CompressionLevel: new(0),
			Lossless:         true,
			Interlace:        true,
		},
	}
	if _, err := svc.TransformImage(t.Context(), upload(), opts); err != nil {
		t.Fatal(err)
	}

	p := <-handler.imageParams

	crop := p.GetCrop()
	if crop.GetLeft() != 10 || crop.GetTop() != 20 || crop.GetWidth() != 300 || crop.GetHeight() != 400 {
		t.Errorf("crop = %+v", crop)
	}

	if p.Rotate == nil || p.GetRotate() != 90 {
		t.Errorf("rotate = %v, want 90", p.Rotate)
	}

	if p.GetResize().GetWidth() != 800 || p.GetResize().GetHeight() != 600 {
		t.Errorf("resize = %+v", p.GetResize())
	}

	if wm := p.GetWatermark(); wm == nil || wm.Opacity == nil || wm.GetOpacity() != 50 {
		t.Errorf("watermark = %+v", wm)
	}

	out := p.GetOutput()
	if out.GetFormat() != commonpb.ImageFormat_IMAGE_FORMAT_WEBP {
		t.Errorf("output format = %v, want WEBP", out.GetFormat())
	}

	if out.Quality == nil || out.GetQuality() != 80 {
		t.Errorf("quality = %v, want 80", out.Quality)
	}

	// CompressionLevel 0 is meaningful and must survive as an explicit 0.
	if out.CompressionLevel == nil || out.GetCompressionLevel() != 0 {
		t.Errorf("compression level = %v, want explicit 0", out.CompressionLevel)
	}

	if !out.GetLossless() || !out.GetInterlace() {
		t.Errorf("lossless/interlace = %v/%v, want true/true", out.GetLossless(), out.GetInterlace())
	}
}

func TestTransformImageNilOptionsSendsEmptyParams(t *testing.T) {
	t.Parallel()

	srv, handler := newCapturingServer(t)
	svc := newService(t, srv.URL)

	if _, err := svc.TransformImage(t.Context(), upload(), nil); err != nil {
		t.Fatal(err)
	}

	p := <-handler.imageParams
	if p == nil {
		t.Fatal("no params frame, want an empty one")
	}

	if p.GetCrop() != nil || p.Rotate != nil || p.GetResize() != nil || p.GetWatermark() != nil || p.GetOutput() != nil {
		t.Errorf("params = %+v, want all fields unset", p)
	}
}

func TestTransformVideoRoundTrip(t *testing.T) {
	t.Parallel()

	// 5 MiB input → 3 upload chunks; same size back → 3 download chunks.
	payload := bytes.Repeat([]byte{0xCD}, 5<<20)

	meta := &rootpullertest.AssetiaVideoMetadata{
		Original: assetia.VideoMetadata{
			Size:       rootpullersdk.ImageSize{Width: 1920, Height: 1080},
			Codec:      "h264",
			Duration:   90 * time.Second,
			FrameRate:  29.97,
			AudioCodec: "aac",
			Extra:      map[string]any{"bitrate": float64(2500000)},
		},
		Result: assetia.VideoMetadata{
			Size:      rootpullersdk.ImageSize{Width: 1280, Height: 720},
			Codec:     "hevc",
			Duration:  90 * time.Second,
			FrameRate: 29.97,
		},
		ProcessingTime: 42 * time.Second,
	}
	wantProgress := []assetia.Progress{
		{Percentage: 10, FPS: 24.5},
		{Percentage: 55.5, FPS: 30},
		{Percentage: 100},
	}

	var gotVideo rootpullersdk.File

	srv := rootpullertest.NewServer(t, &rootpullertest.Assetia{
		VideoMetadata: meta,
		Progress:      wantProgress,
		TransformVideoFunc: func(video rootpullersdk.File) (*rootpullersdk.File, error) {
			gotVideo = video

			return &rootpullersdk.File{Name: "transcoded-" + video.Name, MIMEType: "video/mp4", Data: video.Data}, nil
		},
	})
	svc := newService(t, srv.URL)

	var progress []assetia.Progress

	result, err := svc.TransformVideo(t.Context(),
		rootpullersdk.UploadBytes("clip.mov", "video/quicktime", payload),
		&assetia.TransformVideoOptions{
			Codec:      assetia.H265{Preset: assetia.H265PresetSlow, CRF: new(20)},
			OnProgress: func(p assetia.Progress) { progress = append(progress, p) },
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if gotVideo.Name != "clip.mov" || !bytes.Equal(gotVideo.Data, payload) {
		t.Errorf("server saw video %q with %d bytes", gotVideo.Name, len(gotVideo.Data))
	}

	if result.File.Name != "transcoded-clip.mov" || !bytes.Equal(result.File.Data, payload) {
		t.Errorf("unexpected result file: %q, %d bytes", result.File.Name, len(result.File.Data))
	}

	if !slices.Equal(progress, wantProgress) {
		t.Errorf("progress = %v, want %v", progress, wantProgress)
	}

	if result.ProcessingTime != 42*time.Second {
		t.Errorf("ProcessingTime = %v, want 42s", result.ProcessingTime)
	}

	o := result.Original
	if o.Size != meta.Original.Size || o.Codec != "h264" || o.Duration != 90*time.Second || o.FrameRate != 29.97 {
		t.Errorf("unexpected original metadata: %+v", o)
	}

	if o.AudioCodec != "aac" || o.Extra["bitrate"] != float64(2500000) {
		t.Errorf("original audio/extra = %q/%v", o.AudioCodec, o.Extra)
	}

	r := result.Result
	if r.Size != meta.Result.Size || r.Codec != "hevc" {
		t.Errorf("unexpected result metadata: %+v", r)
	}

	if r.AudioCodec != "" || r.Extra != nil {
		t.Errorf("result audio/extra = %q/%v, want absent optionals", r.AudioCodec, r.Extra)
	}
}

// captureVideoParams runs one TransformVideo call against a capturing
// server and returns the params frame it received.
func captureVideoParams(t *testing.T, opts *assetia.TransformVideoOptions) *assetiapb.TransformVideoRequest_Params {
	t.Helper()

	srv, handler := newCapturingServer(t)
	svc := newService(t, srv.URL)

	if _, err := svc.TransformVideo(t.Context(), upload(), opts); err != nil {
		t.Fatal(err)
	}

	return <-handler.videoParams
}

func TestTransformVideoCodecRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("h265", func(t *testing.T) {
		t.Parallel()

		p := captureVideoParams(t, &assetia.TransformVideoOptions{
			Resize: &assetia.Resize{Width: 1280},
			Codec:  assetia.H265{Preset: assetia.H265PresetVerySlow, CRF: new(0)},
		})
		if p.GetResize().GetWidth() != 1280 || p.GetResize().GetHeight() != 0 {
			t.Errorf("resize = %+v", p.GetResize())
		}

		h265 := p.GetH265()
		if h265 == nil {
			t.Fatalf("codec = %T, want h265 arm", p.GetCodec())
		}

		if h265.Preset == nil || h265.GetPreset() != assetiapb.TransformVideoRequest_H265_PRESET_VERYSLOW {
			t.Errorf("preset = %v, want VERYSLOW", h265.Preset)
		}

		// CRF 0 (lossless) is meaningful and must survive as an explicit 0.
		if h265.Crf == nil || h265.GetCrf() != 0 {
			t.Errorf("crf = %v, want explicit 0", h265.Crf)
		}
	})

	t.Run("h265 default preset stays unset", func(t *testing.T) {
		t.Parallel()

		p := captureVideoParams(t, &assetia.TransformVideoOptions{Codec: assetia.H265{}})

		h265 := p.GetH265()
		if h265 == nil {
			t.Fatalf("codec = %T, want h265 arm", p.GetCodec())
		}

		if h265.Preset != nil || h265.Crf != nil {
			t.Errorf("preset/crf = %v/%v, want nil/nil", h265.Preset, h265.Crf)
		}
	})

	t.Run("av1", func(t *testing.T) {
		t.Parallel()

		// Preset 0 (slowest/best) is meaningful and must survive as an
		// explicit 0.
		p := captureVideoParams(t, &assetia.TransformVideoOptions{Codec: assetia.AV1{Preset: new(0), CRF: new(30)}})

		av1 := p.GetAv1()
		if av1 == nil {
			t.Fatalf("codec = %T, want av1 arm", p.GetCodec())
		}

		if av1.Preset == nil || av1.GetPreset() != 0 {
			t.Errorf("preset = %v, want explicit 0", av1.Preset)
		}

		if av1.Crf == nil || av1.GetCrf() != 30 {
			t.Errorf("crf = %v, want 30", av1.Crf)
		}
	})

	t.Run("nil codec keeps the oneof unset", func(t *testing.T) {
		t.Parallel()

		p := captureVideoParams(t, nil)
		if p.GetCodec() != nil {
			t.Errorf("codec = %T, want unset oneof", p.GetCodec())
		}
	})
}

func TestGeneratePreviewRoundTrip(t *testing.T) {
	t.Parallel()

	// 3 MiB input → 2 upload chunks.
	payload := bytes.Repeat([]byte{0xEF}, 3<<20)

	meta := &rootpullertest.AssetiaPreviewMetadata{
		Source: assetia.MediaMetadata{Video: &assetia.VideoMetadata{
			Size:       rootpullersdk.ImageSize{Width: 1920, Height: 1080},
			Codec:      "h264",
			Duration:   30 * time.Second,
			FrameRate:  25,
			AudioCodec: "aac",
		}},
		Result: assetia.ImageMetadata{
			Size:     rootpullersdk.ImageSize{Width: 320, Height: 180},
			Format:   rootpullersdk.ImageFormatWebP,
			Channels: 3,
		},
		ProcessingTime: 250 * time.Millisecond,
	}

	srv := rootpullertest.NewServer(t, &rootpullertest.Assetia{PreviewMetadata: meta})
	svc := newService(t, srv.URL)

	result, err := svc.GeneratePreview(t.Context(),
		rootpullersdk.UploadBytes("clip.mp4", "video/mp4", payload),
		&assetia.PreviewOptions{Resize: &assetia.Resize{Width: 320}, FrameTime: 1500 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.File.Name != "preview-clip.mp4" || !bytes.Equal(result.File.Data, payload) {
		t.Errorf("unexpected result file: %q, %d bytes", result.File.Name, len(result.File.Data))
	}

	if result.Source.Image != nil || result.Source.Video == nil {
		t.Fatalf("source = %+v, want the video arm", result.Source)
	}

	v := result.Source.Video
	if v.Size != (rootpullersdk.ImageSize{Width: 1920, Height: 1080}) || v.Codec != "h264" || v.AudioCodec != "aac" {
		t.Errorf("unexpected source video: %+v", v)
	}

	if result.Result.Size != meta.Result.Size || result.Result.Format != rootpullersdk.ImageFormatWebP {
		t.Errorf("unexpected result metadata: %+v", result.Result)
	}

	if result.ProcessingTime != 250*time.Millisecond {
		t.Errorf("ProcessingTime = %v, want 250ms", result.ProcessingTime)
	}
}

func TestGeneratePreviewOptionsRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()

		srv, handler := newCapturingServer(t)
		svc := newService(t, srv.URL)

		if _, err := svc.GeneratePreview(t.Context(), upload(), nil); err != nil {
			t.Fatal(err)
		}

		p := <-handler.previewParams
		if p == nil {
			t.Fatal("no params frame, want an empty one")
		}

		if p.GetResize() != nil || p.GetFormat() != commonpb.ImageFormat_IMAGE_FORMAT_UNSPECIFIED || p.FrameTimeMs != nil {
			t.Errorf("params = %+v, want all fields unset", p)
		}
	})

	t.Run("frame time and format", func(t *testing.T) {
		t.Parallel()

		srv, handler := newCapturingServer(t)
		svc := newService(t, srv.URL)

		opts := &assetia.PreviewOptions{
			Format:    rootpullersdk.ImageFormatJPG,
			FrameTime: 1500 * time.Millisecond,
		}
		if _, err := svc.GeneratePreview(t.Context(), upload(), opts); err != nil {
			t.Fatal(err)
		}

		p := <-handler.previewParams
		if p.GetFormat() != commonpb.ImageFormat_IMAGE_FORMAT_JPG {
			t.Errorf("format = %v, want JPG", p.GetFormat())
		}

		if p.FrameTimeMs == nil || p.GetFrameTimeMs() != 1500 {
			t.Errorf("frame time = %v, want 1500ms", p.FrameTimeMs)
		}
	})
}

func TestProbeMediaRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("image", func(t *testing.T) {
		t.Parallel()

		// 3 MiB input → 2 upload chunks.
		payload := bytes.Repeat([]byte{0x11}, 3<<20)

		var gotMedia rootpullersdk.File

		srv := rootpullertest.NewServer(t, &rootpullertest.Assetia{
			ProbeFunc: func(media rootpullersdk.File) (*assetia.MediaMetadata, error) {
				gotMedia = media

				return &assetia.MediaMetadata{Image: &assetia.ImageMetadata{
					Size:            rootpullersdk.ImageSize{Width: 640, Height: 480},
					Format:          rootpullersdk.ImageFormatPNG,
					ColorSpace:      "srgb",
					HasAlpha:        true,
					Channels:        4,
					EXIFOrientation: 1,
					Extra:           map[string]any{"depth": float64(8)},
				}}, nil
			},
		})
		svc := newService(t, srv.URL)

		meta, err := svc.ProbeMedia(t.Context(), rootpullersdk.UploadBytes("pic.png", "image/png", payload))
		if err != nil {
			t.Fatal(err)
		}

		if gotMedia.Name != "pic.png" || !bytes.Equal(gotMedia.Data, payload) {
			t.Errorf("server saw media %q with %d bytes", gotMedia.Name, len(gotMedia.Data))
		}

		if meta.Video != nil || meta.Image == nil {
			t.Fatalf("metadata = %+v, want the image arm", meta)
		}

		img := meta.Image
		if img.Size != (rootpullersdk.ImageSize{Width: 640, Height: 480}) || img.Format != rootpullersdk.ImageFormatPNG {
			t.Errorf("unexpected image metadata: %+v", img)
		}

		if !img.HasAlpha || img.Channels != 4 || img.EXIFOrientation != 1 {
			t.Errorf("unexpected image flags: %+v", img)
		}

		if img.Extra["depth"] != float64(8) {
			t.Errorf("Extra = %v", img.Extra)
		}
	})

	t.Run("video", func(t *testing.T) {
		t.Parallel()

		srv := rootpullertest.NewServer(t, &rootpullertest.Assetia{
			ProbeFunc: func(rootpullersdk.File) (*assetia.MediaMetadata, error) {
				return &assetia.MediaMetadata{Video: &assetia.VideoMetadata{
					Size:      rootpullersdk.ImageSize{Width: 3840, Height: 2160},
					Codec:     "av1",
					Duration:  2 * time.Minute,
					FrameRate: 60,
					Extra:     map[string]any{"streams": float64(1)},
				}}, nil
			},
		})
		svc := newService(t, srv.URL)

		meta, err := svc.ProbeMedia(t.Context(), rootpullersdk.UploadBytes("clip.webm", "video/webm", []byte{1, 2, 3}))
		if err != nil {
			t.Fatal(err)
		}

		if meta.Image != nil || meta.Video == nil {
			t.Fatalf("metadata = %+v, want the video arm", meta)
		}

		v := meta.Video
		if v.Size != (rootpullersdk.ImageSize{Width: 3840, Height: 2160}) || v.Codec != "av1" || v.Duration != 2*time.Minute || v.FrameRate != 60 {
			t.Errorf("unexpected video metadata: %+v", v)
		}

		if v.AudioCodec != "" {
			t.Errorf("AudioCodec = %q, want empty for absent proto optional", v.AudioCodec)
		}

		if v.Extra["streams"] != float64(1) {
			t.Errorf("Extra = %v", v.Extra)
		}
	})
}

func TestInvalidArgumentsFailLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	_, err := svc.TransformImage(t.Context(), upload(),
		&assetia.TransformImageOptions{Output: &assetia.Output{Format: rootpullersdk.ImageFormat("bmp")}})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("TransformImage err = %v, want ErrInvalidArgument", err)
	}

	_, err = svc.GeneratePreview(t.Context(), upload(),
		&assetia.PreviewOptions{Format: rootpullersdk.ImageFormat("bmp")})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("GeneratePreview err = %v, want ErrInvalidArgument", err)
	}

	_, err = svc.TransformVideo(t.Context(), upload(),
		&assetia.TransformVideoOptions{Codec: assetia.H265{Preset: assetia.H265Preset("warp")}})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("TransformVideo preset err = %v, want ErrInvalidArgument", err)
	}

	// Codec variants are the value types; a pointer is a wrong variant.
	_, err = svc.TransformVideo(t.Context(), upload(),
		&assetia.TransformVideoOptions{Codec: &assetia.H265{}})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("TransformVideo codec err = %v, want ErrInvalidArgument", err)
	}
}

func TestTransformImageServerError(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Assetia{
		TransformImageFunc: func(rootpullersdk.File, *rootpullersdk.File) (*rootpullersdk.File, error) {
			return nil, errTransformFailed
		},
	})
	svc := newService(t, srv.URL)

	_, err := svc.TransformImage(t.Context(), upload(), nil)
	if err == nil {
		t.Fatal("want error")
	}

	if _, ok := errors.AsType[*rootpullersdk.Error](err); !ok {
		t.Fatalf("err = %#v, want *rootpullersdk.Error", err)
	}
}
