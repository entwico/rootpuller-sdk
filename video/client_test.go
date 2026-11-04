package video_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/video"
)

const (
	mimeMP4   = "video/mp4"
	videoName = "video_0.mp4"
)

var errNotServed = errors.New("model not served")

func newService(t *testing.T, fake *rootpullertest.Video, opts ...video.Option) *video.Service {
	t.Helper()

	srv := rootpullertest.NewServer(t, fake)

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	return video.NewService(sdk, opts...)
}

func upload(name string, data []byte) rootpullersdk.Upload {
	return rootpullersdk.UploadBytes(name, "image/png", data)
}

func TestGenerateTextToVideo(t *testing.T) {
	t.Parallel()

	var got rootpullertest.VideoRequest

	clip := bytes.Repeat([]byte{0x42}, 5<<20)
	svc := newService(t, &rootpullertest.Video{
		GenerateFunc: func(req rootpullertest.VideoRequest) (rootpullersdk.File, error) {
			got = req

			return rootpullersdk.File{Name: videoName, MIMEType: mimeMP4, Data: clip}, nil
		},
		Progress: []video.Progress{{Stage: video.StageQueued, Message: "queued: 2 job(s) ahead"}},
		Metadata: &video.Metadata{
			Model: video.ModelLTX25, Mode: video.ModeTextToVideo, Seed: 7,
			Size: rootpullersdk.ImageSize{Width: 1024, Height: 576}, Duration: 4040 * time.Millisecond,
		},
	}, video.WithComfyUIDeployment("cluster"))

	var progress []video.Progress

	res, err := svc.Generate(t.Context(), "drone shot over a coast", &video.GenerateOptions{
		FrameCount: new(97),
		Seed:       new(int64(7)),
		OnProgress: func(p video.Progress) { progress = append(progress, p) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Model != video.ModelLTX25 {
		t.Errorf("model = %q, want the default %q", got.Model, video.ModelLTX25)
	}

	if got.ComfyUIDeployment != "cluster" {
		t.Errorf("deployment header = %q, want cluster", got.ComfyUIDeployment)
	}

	if got.FrameCount == nil || *got.FrameCount != 97 || got.FirstFrame != nil {
		t.Errorf("request = %+v", got)
	}

	if !bytes.Equal(res.Video.Data, clip) || res.Video.MIMEType != mimeMP4 {
		t.Error("video was not reassembled from its chunks")
	}

	if res.Metadata.Seed != 7 || res.Metadata.Duration != 4040*time.Millisecond || res.Metadata.Size.Width != 1024 {
		t.Errorf("metadata = %+v", res.Metadata)
	}

	if len(progress) != 1 || progress[0].Stage != video.StageQueued {
		t.Errorf("progress = %+v", progress)
	}
}

func TestGenerateFirstLastFrame(t *testing.T) {
	t.Parallel()

	var got rootpullertest.VideoRequest

	first := bytes.Repeat([]byte{0x01}, 3<<20)
	last := []byte{0x02}

	svc := newService(t, &rootpullertest.Video{
		GenerateFunc: func(req rootpullertest.VideoRequest) (rootpullersdk.File, error) {
			got = req

			return rootpullersdk.File{Name: videoName, MIMEType: mimeMP4, Data: []byte{1}}, nil
		},
	})

	ctx := rootpullersdk.ContextWithComfyUIDeployment(t.Context(), "local")

	res, err := svc.Generate(ctx, "the dough is rolled out", &video.GenerateOptions{
		FirstFrame: new(upload("raw.png", first)),
		LastFrame:  new(upload("rolled.png", last)),
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.FirstFrame == nil || !bytes.Equal(got.FirstFrame.Data, first) || !bytes.Equal(got.LastFrame.Data, last) {
		t.Error("frames arrived wrong")
	}

	if got.ComfyUIDeployment != "local" {
		t.Errorf("deployment header = %q, want local", got.ComfyUIDeployment)
	}

	if res.Metadata.Mode != video.ModeFirstLastFrame {
		t.Errorf("mode = %q", res.Metadata.Mode)
	}
}

func TestGenerateLastWithoutFirstFailsLocally(t *testing.T) {
	t.Parallel()

	called := false
	svc := newService(t, &rootpullertest.Video{
		GenerateFunc: func(rootpullertest.VideoRequest) (rootpullersdk.File, error) {
			called = true

			return rootpullersdk.File{}, nil
		},
	})

	_, err := svc.Generate(t.Context(), "x", &video.GenerateOptions{LastFrame: new(upload("a.png", []byte{1}))})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}

	if called {
		t.Error("the request must fail before any RPC")
	}
}

func TestGenerateTalking(t *testing.T) {
	t.Parallel()

	var got rootpullertest.TalkingRequest

	svc := newService(t, &rootpullertest.Video{
		TalkingFunc: func(req rootpullertest.TalkingRequest) (rootpullersdk.File, error) {
			got = req

			return rootpullersdk.File{Name: videoName, MIMEType: mimeMP4, Data: []byte{9}}, nil
		},
	})

	voice := bytes.Repeat([]byte{0x07}, 5<<20)

	res, err := svc.GenerateTalking(t.Context(), "a friendly presenter",
		upload("portrait.png", []byte{0x89, 0x50}),
		rootpullersdk.UploadBytes("voice.wav", "audio/wav", voice),
		&video.TalkingOptions{Seed: new(int64(3))},
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.Model != video.ModelInfiniteTalk || got.Seed == nil || *got.Seed != 3 {
		t.Errorf("request = %+v", got)
	}

	if !bytes.Equal(got.Audio.Data, voice) || len(got.Portrait.Data) != 2 {
		t.Error("portrait or audio arrived wrong")
	}

	if res.Metadata.Mode != video.ModeTalkingHead || len(res.Video.Data) != 1 {
		t.Errorf("result = %+v", res.Metadata)
	}
}

func TestGenerateServerError(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Video{
		GenerateFunc: func(rootpullertest.VideoRequest) (rootpullersdk.File, error) {
			return rootpullersdk.File{}, connect.NewError(connect.CodeNotFound, errNotServed)
		},
	})

	_, err := svc.Generate(t.Context(), "x", nil)
	if !errors.Is(err, rootpullersdk.ErrNotFound) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

func TestGenerateTalkingSpeech(t *testing.T) {
	t.Parallel()

	var got rootpullertest.TalkingRequest

	svc := newService(t, &rootpullertest.Video{
		TalkingFunc: func(req rootpullertest.TalkingRequest) (rootpullersdk.File, error) {
			got = req

			return rootpullersdk.File{Name: videoName, MIMEType: mimeMP4, Data: []byte{9}}, nil
		},
		Progress: []video.Progress{{Stage: video.StageSpeaking, Message: "speaking the text"}},
	})

	var progress []video.Progress

	res, err := svc.GenerateTalkingSpeech(t.Context(), "a friendly presenter",
		upload("portrait.png", []byte{0x89, 0x50}),
		video.Speech{Voice: "opaque-ref", Text: "Hallo zusammen", Language: "de"},
		&video.TalkingOptions{OnProgress: func(p video.Progress) { progress = append(progress, p) }},
	)
	if err != nil {
		t.Fatal(err)
	}

	want := video.Speech{Voice: "opaque-ref", Text: "Hallo zusammen", Language: "de"}
	if got.Speech == nil || *got.Speech != want || len(got.Audio.Data) != 0 || len(got.Portrait.Data) != 2 {
		t.Errorf("request = %+v", got)
	}

	if len(progress) != 1 || progress[0].Stage != video.StageSpeaking {
		t.Errorf("progress = %+v", progress)
	}

	if len(res.Video.Data) != 1 {
		t.Errorf("result = %+v", res.Metadata)
	}
}

func TestGenerateTalkingSpeechNeedsAllFields(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Video{})

	_, err := svc.GenerateTalkingSpeech(t.Context(), "p", upload("p.png", []byte{1}), video.Speech{Voice: "r", Text: "t"}, nil)

	var ae *rootpullersdk.Error
	if !errors.As(err, &ae) || ae.Code != connect.CodeInvalidArgument {
		t.Fatalf("err = %v", err)
	}
}

func TestFakeRejectsWhatTheServerRejectsForSpeech(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Video{})
	portrait := upload("p.png", []byte{1})

	for name, tc := range map[string]struct {
		prompt string
		speech video.Speech
	}{
		"blank prompt":          {"  ", video.Speech{Voice: "r", Text: "Hallo", Language: "de"}},
		"BCP-47 language":       {"p", video.Speech{Voice: "r", Text: "Hallo", Language: "de-DE"}},
		"whitespace text":       {"p", video.Speech{Voice: "r", Text: "   ", Language: "de"}},
		"text over 40000 runes": {"p", video.Speech{Voice: "r", Text: strings.Repeat("a", 40_001), Language: "de"}},
	} {
		_, err := svc.GenerateTalkingSpeech(t.Context(), tc.prompt, portrait, tc.speech, nil)

		var ae *rootpullersdk.Error
		if !errors.As(err, &ae) || ae.Code != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
}
