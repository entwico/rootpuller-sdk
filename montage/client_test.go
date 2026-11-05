package montage_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/montage"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

const mimeMP4 = "video/mp4"

var errDisk = errors.New("disk gone")

func newService(t *testing.T, fake *rootpullertest.Montage) *montage.Service {
	t.Helper()

	srv := rootpullertest.NewServer(t, fake)

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	return montage.NewService(sdk)
}

func requireCode(t *testing.T, err error, code connect.Code) {
	t.Helper()

	ae, ok := errors.AsType[*rootpullersdk.Error](err)
	if !ok {
		t.Fatalf("want *rootpullersdk.Error with %v, got %v", code, err)
	}

	if ae.Code != code {
		t.Fatalf("code = %v, want %v (%v)", ae.Code, code, err)
	}
}

func asset(id string, data []byte) montage.Asset {
	return montage.Asset{ID: id, Upload: rootpullersdk.UploadBytes(id+".mp4", mimeMP4, data)}
}

func reel() montage.Timeline {
	return montage.Timeline{
		Segments: []montage.Segment{
			{Asset: "shot1", Duration: 4 * time.Second, Sound: montage.SoundDuck, GainDB: new(-18.0),
				Transition: montage.Transition{Kind: montage.TransitionCrossfade, Duration: 500 * time.Millisecond}},
			{Asset: "shot2", Start: time.Second, Duration: 5 * time.Second, Sound: montage.SoundMute},
		},
		AudioTracks: []montage.AudioTrack{
			{Asset: "voice", At: 300 * time.Millisecond, FadeOut: 200 * time.Millisecond},
			{Asset: "music", GainDB: -20, Loop: true},
		},
		Overlays: []montage.Overlay{
			{Asset: "caption", At: time.Second, Duration: 3 * time.Second, Anchor: montage.AnchorBottom,
				MarginY: 120, Width: 480, Opacity: new(0.9)},
		},
		Output:       montage.Output{Width: 576, Height: 1024, Quality: montage.QualityHigh, FadeOut: time.Second, AudioBitrate: 96},
		DurationMode: montage.DurationAudio,
	}
}

func reelAssets() []montage.Asset {
	// the voice is larger than one 2 MiB chunk, so it travels in several.
	return []montage.Asset{
		asset("shot1", []byte("SHOT1")),
		asset("shot2", []byte("SHOT2")),
		asset("voice", bytes.Repeat([]byte("v"), 5<<20)),
		asset("music", []byte("MUSIC")),
		asset("caption", []byte("PNG")),
	}
}

func TestRender(t *testing.T) {
	t.Parallel()

	var got rootpullertest.MontageRequest

	svc := newService(t, &rootpullertest.Montage{
		RenderFunc: func(req rootpullertest.MontageRequest) (rootpullersdk.File, error) {
			got = req

			return rootpullersdk.File{Name: "montage_0.mp4", MIMEType: mimeMP4, Data: []byte("MP4")}, nil
		},
		Metadata: &montage.Metadata{
			Duration: 30 * time.Second, Width: 576, Height: 1024, FPS: 24,
			AudioSampleRate: 48000, AudioChannels: 2, AudioBitrate: 96,
		},
		Progress: []montage.Progress{
			{Stage: montage.StageQueued},
			{Stage: montage.StageRendering, Percentage: 0.5},
		},
	})

	var progress []montage.Progress

	res, err := svc.Render(t.Context(), reel(), reelAssets(), &montage.RenderOptions{
		OnProgress: func(p montage.Progress) { progress = append(progress, p) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if string(res.Video.Data) != "MP4" || res.Video.MIMEType != mimeMP4 {
		t.Fatalf("video = %q %q", res.Video.Data, res.Video.MIMEType)
	}

	if res.Metadata.Duration != 30*time.Second || res.Metadata.Width != 576 ||
		res.Metadata.AudioSampleRate != 48000 || res.Metadata.AudioChannels != 2 || res.Metadata.AudioBitrate != 96 {
		t.Fatalf("metadata = %+v", res.Metadata)
	}

	if len(progress) != 2 || progress[1].Stage != montage.StageRendering {
		t.Fatalf("progress = %+v", progress)
	}

	// the timeline arrives as sent.
	want := reel()
	if g := got.Timeline; !reflect.DeepEqual(g.Segments, want.Segments) || g.DurationMode != want.DurationMode ||
		g.Output != want.Output || g.AudioTracks[0] != want.AudioTracks[0] || g.AudioTracks[1] != want.AudioTracks[1] {
		t.Fatalf("timeline = %+v,\nwant %+v", g, want)
	}

	o := got.Timeline.Overlays[0]
	if o.Anchor != montage.AnchorBottom || o.Width != 480 || o.MarginY != 120 || o.Opacity == nil || *o.Opacity < 0.89 {
		t.Fatalf("overlay = %+v", o)
	}

	if len(got.Assets) != 5 || len(got.Assets["voice"].Data) != 5<<20 || string(got.Assets["shot1"].Data) != "SHOT1" {
		t.Fatalf("assets = %d, voice %d bytes", len(got.Assets), len(got.Assets["voice"].Data))
	}
}

func TestRenderRejectsLocally(t *testing.T) {
	t.Parallel()

	var called atomic.Bool

	svc := newService(t, &rootpullertest.Montage{
		RenderFunc: func(rootpullertest.MontageRequest) (rootpullersdk.File, error) {
			called.Store(true)

			return rootpullersdk.File{}, nil
		},
	})

	clip := montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}}

	cases := []struct {
		name     string
		timeline montage.Timeline
		assets   []montage.Asset
	}{
		{"unknown sound", montage.Timeline{Segments: []montage.Segment{{Asset: "a", Sound: "loud"}}}, []montage.Asset{asset("a", []byte("x"))}},
		{"unknown anchor", montage.Timeline{
			Segments: []montage.Segment{{Asset: "a"}}, Overlays: []montage.Overlay{{Asset: "a", Anchor: "middle"}},
		}, []montage.Asset{asset("a", []byte("x"))}},
		{"missing asset", clip, nil},
		{"extra asset", clip, []montage.Asset{asset("a", []byte("x")), asset("b", []byte("y"))}},
		{"asset twice", clip, []montage.Asset{asset("a", []byte("x")), asset("a", []byte("x"))}},
		{"asset without id", clip, []montage.Asset{asset("", []byte("x"))}},
		{"timeline entry without asset", montage.Timeline{Segments: []montage.Segment{{}}}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.Render(t.Context(), tc.timeline, tc.assets, nil)
			requireCode(t, err, connect.CodeInvalidArgument)

			// the SDK's own checks say "montage:"; the fake's would not, so a
			// rejection that only the server made would fail here.
			if !strings.HasPrefix(err.Error(), "montage:") && !strings.Contains(err.Error(), ": montage:") {
				t.Fatalf("rejected by the server, not locally: %v", err)
			}
		})
	}

	t.Cleanup(func() {
		if called.Load() {
			t.Error("a request that fails locally reached the server")
		}
	})
}

func TestRenderTheFakeEnforcesTheRules(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Montage{})

	cases := []struct {
		name     string
		timeline montage.Timeline
	}{
		{"explicit without duration", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, DurationMode: montage.DurationExplicit}},
		{"duration without explicit", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, Duration: time.Second}},
		{"audio mode with only loops", montage.Timeline{
			Segments: []montage.Segment{{Asset: "a"}}, AudioTracks: []montage.AudioTrack{{Asset: "a", Loop: true}},
			DurationMode: montage.DurationAudio,
		}},
		{"crossfade without length", montage.Timeline{Segments: []montage.Segment{
			{Asset: "a", Transition: montage.Transition{Kind: montage.TransitionCrossfade}}, {Asset: "a"},
		}}},
		{"odd size", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, Output: montage.Output{Width: 575, Height: 1024}}},
		{"half a size", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, Output: montage.Output{Width: 576}}},
		{"no segments", montage.Timeline{AudioTracks: []montage.AudioTrack{{Asset: "a"}}}},
		{"duck without gain", montage.Timeline{Segments: []montage.Segment{{Asset: "a", Sound: montage.SoundDuck}}}},
		{"gain without duck", montage.Timeline{Segments: []montage.Segment{{Asset: "a", GainDB: new(-6.0)}}}},
		{"NaN gain", montage.Timeline{Segments: []montage.Segment{{Asset: "a", Sound: montage.SoundDuck, GainDB: new(math.NaN())}}}},
		{"overlay wider than the cap", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, Overlays: []montage.Overlay{{Asset: "a", Width: 5000}}}},
		{"NaN opacity", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, Overlays: []montage.Overlay{{Asset: "a", Opacity: new(math.NaN())}}}},
		{"negative track start", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, AudioTracks: []montage.AudioTrack{{Asset: "a", At: -time.Second}}}},
		{"negative overlay margin", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, Overlays: []montage.Overlay{{Asset: "a", MarginX: -4}}}},
		{"audio bitrate below 32", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, Output: montage.Output{AudioBitrate: 16}}},
		{"audio bitrate above 320", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, Output: montage.Output{AudioBitrate: 512}}},
		{"negative audio bitrate", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}, Output: montage.Output{AudioBitrate: -1}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// each subtest reads its own upload: they run in parallel.
			_, err := svc.Render(t.Context(), tc.timeline, []montage.Asset{asset("a", []byte("x"))}, nil)
			requireCode(t, err, connect.CodeInvalidArgument)
		})
	}
}

// failingReader yields data, then fails.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}

	n := copy(p, r.data)
	r.data = r.data[n:]

	return n, nil
}

func TestRenderUnreadableAssetRendersNothing(t *testing.T) {
	t.Parallel()

	var rendered atomic.Bool

	svc := newService(t, &rootpullertest.Montage{
		RenderFunc: func(rootpullertest.MontageRequest) (rootpullersdk.File, error) {
			rendered.Store(true)

			return rootpullersdk.File{}, nil
		},
	})

	broken := montage.Asset{ID: "a", Upload: rootpullersdk.Upload{
		Name: "a.mp4", MIMEType: mimeMP4, Content: &failingReader{data: bytes.Repeat([]byte("x"), 3<<20), err: errDisk},
	}}

	_, err := svc.Render(t.Context(), montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}}, []montage.Asset{broken}, nil)
	if !errors.Is(err, errDisk) {
		t.Fatalf("err = %v, want the read error", err)
	}

	if rendered.Load() {
		t.Fatal("the server rendered a montage whose upload failed")
	}
}

func TestReferencedAssets(t *testing.T) {
	t.Parallel()

	got := montage.ReferencedAssets(reel())
	want := []string{"shot1", "shot2", "voice", "music", "caption"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestGetCapabilities(t *testing.T) {
	t.Parallel()

	caps, err := newService(t, &rootpullertest.Montage{}).GetCapabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if caps.MaxAssets != 24 || caps.MaxOutput != 10*time.Minute || len(caps.VideoMIMETypes) == 0 {
		t.Fatalf("caps = %+v", caps)
	}
}

func TestRenderTheFakeEnforcesUploadRules(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Montage{Capabilities: &montage.Capabilities{
		MaxAssets: 2, MaxAssetBytes: 4, MaxTotalBytes: 6, MaxSegments: 3,
	}})

	cases := []struct {
		name     string
		timeline montage.Timeline
		assets   []montage.Asset
	}{
		{"empty asset", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}}, []montage.Asset{asset("a", nil)}},
		{"too many assets", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}, {Asset: "b"}, {Asset: "c"}}},
			[]montage.Asset{asset("a", []byte("1")), asset("b", []byte("2")), asset("c", []byte("3"))}},
		{"asset too large", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}}}, []montage.Asset{asset("a", []byte("12345"))}},
		{"assets too large together", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}, {Asset: "b"}}},
			[]montage.Asset{asset("a", []byte("1234")), asset("b", []byte("1234"))}},
		{"too many segments", montage.Timeline{Segments: []montage.Segment{{Asset: "a"}, {Asset: "a"}, {Asset: "a"}, {Asset: "a"}}},
			[]montage.Asset{asset("a", []byte("1"))}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.Render(t.Context(), tc.timeline, tc.assets, nil)
			requireCode(t, err, connect.CodeInvalidArgument)
		})
	}
}

// A params frame that cannot be encoded fails before the request starts; the
// call must return its error, not hang closing a response that never began.
func TestRenderUnsendableParamsDoesNotHang(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Montage{})
	bad := "\xff" // not UTF-8, which a proto3 string field refuses

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := svc.Render(ctx, montage.Timeline{Segments: []montage.Segment{{Asset: bad}}},
		[]montage.Asset{asset(bad, []byte("x"))}, nil)
	if err == nil {
		t.Fatal("an unencodable params frame was accepted")
	}

	if ctx.Err() != nil {
		t.Fatal("Render hung instead of failing")
	}
}

func TestRenderTheFakeReportsTheServersAudio(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Montage{})

	for _, tc := range []struct {
		output  montage.Output
		bitrate int
	}{
		{montage.Output{}, 128},
		{montage.Output{Quality: montage.QualityHigh}, 192},
		{montage.Output{Quality: montage.QualityHigh, AudioBitrate: 64}, 64},
	} {
		timeline := montage.Timeline{Segments: []montage.Segment{{Asset: "a", Duration: time.Second}}, Output: tc.output}

		res, err := svc.Render(t.Context(), timeline, []montage.Asset{asset("a", []byte("x"))}, nil)
		if err != nil {
			t.Fatal(err)
		}

		if m := res.Metadata; m.AudioSampleRate != 48000 || m.AudioChannels != 2 || m.AudioBitrate != tc.bitrate {
			t.Fatalf("output %+v: metadata = %+v", tc.output, m)
		}
	}
}
