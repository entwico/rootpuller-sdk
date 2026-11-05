package voice_test

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/voice"
)

const (
	description = "speaking German, a woman, early thirties, warm mid-range voice"
	sampleText  = "Hallo, ich bin Lena. Schön, dass du da bist. Heute zeige ich euch, wie ich meinen Morgen plane: mit Kaffee und Musik."
	german      = "de"
	voiceName   = "Lena"
	designModel = "design-v3"
	speechModel = "speech-v4"
	mimeMP3     = "audio/mpeg"
	opaqueRef   = "opaque-ref"
)

var (
	errOther = errors.New("other")
	errDisk  = errors.New("disk gone")
)

func newService(t *testing.T, fake *rootpullertest.Voice) *voice.Service {
	t.Helper()

	srv := rootpullertest.NewServer(t, fake)

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	return voice.NewService(sdk)
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

func TestDesign(t *testing.T) {
	t.Parallel()

	var got rootpullertest.VoiceDesignRequest

	expires := time.Unix(1_900_000_000, 0)
	big := bytes.Repeat([]byte{0xab}, 3<<20) // spans two chunks

	svc := newService(t, &rootpullertest.Voice{
		DesignFunc: func(req rootpullertest.VoiceDesignRequest) (*voice.DesignResult, error) {
			got = req

			return &voice.DesignResult{
				Model:      designModel,
				SampleText: req.SampleText,
				Usage:      voice.Usage{Characters: 44, EstimatedCostMicros: 8800, CurrencyCode: "USD"},
				Previews: []voice.Preview{
					{Handle: "h0", Audio: rootpullersdk.File{MIMEType: mimeMP3, Data: []byte{1}}, Duration: 4 * time.Second},
					{Handle: "h1", Audio: rootpullersdk.File{MIMEType: mimeMP3, Data: big}, ExpiresAt: &expires},
				},
			}, nil
		},
		Progress: []voice.Progress{{Stage: voice.StageGenerating, Percentage: 0.5, Message: "preview 1 of 2"}},
	})

	var progress []voice.Progress

	two := 2

	res, err := svc.Design(t.Context(), description, sampleText, german, &voice.DesignOptions{
		Previews:   &two,
		Model:      designModel,
		OnProgress: func(p voice.Progress) { progress = append(progress, p) },
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Description != description || got.Language != german || *got.Previews != 2 || *got.Model != designModel {
		t.Fatalf("request = %+v", got)
	}

	if len(res.Previews) != 2 {
		t.Fatalf("previews = %d", len(res.Previews))
	}

	if p := res.Previews[1]; p.Handle != "h1" || p.Index != 1 || !bytes.Equal(p.Audio.Data, big) ||
		p.Audio.Name != "preview_1.mp3" || p.ExpiresAt == nil || !p.ExpiresAt.Equal(expires) {
		t.Fatalf("preview 1 = %+v", p)
	}

	if res.Previews[0].Duration != 4*time.Second || res.Previews[0].ExpiresAt != nil {
		t.Fatalf("preview 0 = %+v", res.Previews[0])
	}

	if res.Usage.Characters != 44 || res.Usage.CurrencyCode != "USD" {
		t.Fatalf("usage = %+v", res.Usage)
	}

	if len(progress) != 1 || progress[0].Message != "preview 1 of 2" {
		t.Fatalf("progress = %+v", progress)
	}
}

func TestDesign_PreviewsOutOfRangeFailLocally(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{
		DesignFunc: func(rootpullertest.VoiceDesignRequest) (*voice.DesignResult, error) {
			t.Fatal("the request reached the server")

			return nil, nil
		},
	})

	for _, n := range []int{0, voice.MaxPreviews + 1} {
		_, err := svc.Design(t.Context(), description, sampleText, german, &voice.DesignOptions{Previews: &n})
		requireCode(t, err, connect.CodeInvalidArgument)
	}
}

func TestSave_FromHandleWithSample(t *testing.T) {
	t.Parallel()

	var got rootpullertest.VoiceSaveRequest

	slots, limit := 4, 10
	sample := bytes.Repeat([]byte{0x11}, 5<<20)

	svc := newService(t, &rootpullertest.Voice{
		SaveFunc: func(req rootpullertest.VoiceSaveRequest) (*voice.SaveResult, error) {
			got = req

			return &voice.SaveResult{Ref: opaqueRef, SlotsUsed: &slots, SlotsLimit: &limit}, nil
		},
	})

	up := rootpullersdk.UploadBytes("preview_0.mp3", mimeMP3, sample)

	res, err := svc.Save(t.Context(), voiceName, &voice.SaveOptions{Description: "warm", Handle: "h0", Sample: &up})
	if err != nil {
		t.Fatal(err)
	}

	if got.Name != voiceName || got.Description != "warm" || got.Handle == nil || *got.Handle != "h0" {
		t.Fatalf("request = %+v", got)
	}

	if got.Sample == nil || !bytes.Equal(got.Sample.Data, sample) || got.Sample.Name != "preview_0.mp3" {
		t.Fatal("the sample did not arrive whole")
	}

	if res.Ref != opaqueRef || res.Cloned || *res.SlotsUsed != 4 || *res.SlotsLimit != 10 {
		t.Fatalf("result = %+v", res)
	}
}

func TestSave_SampleOnlyIsCloned(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{})

	up := rootpullersdk.UploadBytes("s.mp3", mimeMP3, []byte{1, 2})

	res, err := svc.Save(t.Context(), voiceName, &voice.SaveOptions{Sample: &up})
	if err != nil {
		t.Fatal(err)
	}

	if !res.Cloned || res.Ref == "" {
		t.Fatalf("result = %+v", res)
	}
}

func TestSave_NothingToSaveFailsLocally(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{})

	_, err := svc.Save(t.Context(), voiceName, nil)
	requireCode(t, err, connect.CodeInvalidArgument)

	// a server-side rejection names the procedure; only the local check has none.
	if ae, _ := errors.AsType[*rootpullersdk.Error](err); ae.Procedure != "" {
		t.Fatalf("the error came from the server (%s), not the local check", ae.Procedure)
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

func TestSave_UnsendableChunkSavesNothing(t *testing.T) {
	t.Parallel()

	var saved atomic.Bool

	svc := newService(t, &rootpullertest.Voice{
		SaveFunc: func(rootpullertest.VoiceSaveRequest) (*voice.SaveResult, error) {
			saved.Store(true)

			return &voice.SaveResult{Ref: opaqueRef}, nil
		},
	})

	// a name that is not valid UTF-8 cannot be marshalled into the chunk, so
	// Send fails on this side after the params frame went out.
	sample := rootpullersdk.UploadBytes("s\xff.mp3", mimeMP3, []byte{1, 2, 3})

	_, err := svc.Save(t.Context(), voiceName, &voice.SaveOptions{Handle: "h0", Sample: &sample})
	if err == nil {
		t.Fatal("Save succeeded with a sample that cannot be sent")
	}

	if saved.Load() {
		t.Fatal("the server saved a voice from an upload that failed; its ref is lost")
	}
}

func TestSave_UnsendableParamsReportTheCause(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{})

	// a name that is not valid UTF-8 fails to marshal on this side; the caller
	// sees that, not the server's "missing params" after a clean close.
	_, err := svc.Save(t.Context(), "Lena\xff", &voice.SaveOptions{Handle: "h0"})

	ae, ok := errors.AsType[*rootpullersdk.Error](err)
	if !ok || ae.Code == connect.CodeInvalidArgument || strings.Contains(ae.Message, "params") {
		t.Fatalf("err = %v, want the client-side marshal failure", err)
	}
}

func TestSave_UnreadableSampleSavesNothing(t *testing.T) {
	t.Parallel()

	for name, data := range map[string][]byte{
		"fails before the first chunk": nil,
		"fails after a chunk":          bytes.Repeat([]byte{1}, 3<<20),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var saved atomic.Bool

			svc := newService(t, &rootpullertest.Voice{
				SaveFunc: func(rootpullertest.VoiceSaveRequest) (*voice.SaveResult, error) {
					saved.Store(true)

					return &voice.SaveResult{Ref: opaqueRef}, nil
				},
			})

			sample := rootpullersdk.Upload{Name: "s.mp3", MIMEType: mimeMP3, Content: &failingReader{data: data, err: errDisk}}

			_, err := svc.Save(t.Context(), voiceName, &voice.SaveOptions{Handle: "h0", Sample: &sample})
			if !errors.Is(err, errDisk) {
				t.Fatalf("err = %v, want the read error", err)
			}

			if saved.Load() {
				t.Fatal("the server saved a voice from an upload that failed; its ref is lost")
			}
		})
	}
}

func TestFakeRejectsWhatTheServerRejects(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{})

	sample := func(data []byte) *rootpullersdk.Upload {
		up := rootpullersdk.UploadBytes("s.mp3", mimeMP3, data)

		return &up
	}

	cases := map[string]func() error{
		"empty name": func() error {
			_, err := svc.Save(t.Context(), "", &voice.SaveOptions{Sample: sample([]byte{1})})

			return err
		},
		"whitespace name": func() error {
			_, err := svc.Save(t.Context(), "   ", &voice.SaveOptions{Sample: sample([]byte{1})})

			return err
		},
		"name over 100 characters": func() error {
			_, err := svc.Save(t.Context(), strings.Repeat("n", 101), &voice.SaveOptions{Sample: sample([]byte{1})})

			return err
		},
		"empty sample with a handle": func() error {
			_, err := svc.Save(t.Context(), voiceName, &voice.SaveOptions{Handle: "h0", Sample: sample(nil)})

			return err
		},
		"sample over 64 MiB": func() error {
			_, err := svc.Save(t.Context(), voiceName, &voice.SaveOptions{Sample: sample(make([]byte, 64<<20+1))})

			return err
		},
		"description over 40000 characters": func() error {
			_, err := svc.Save(t.Context(), voiceName, &voice.SaveOptions{Handle: "h0", Description: strings.Repeat("d", 40_001)})

			return err
		},
		"empty ref": func() error { return svc.Delete(t.Context(), "") },
		"speech over the model limit": func() error {
			_, err := svc.Speak(t.Context(), opaqueRef, strings.Repeat("a", 5001), german, nil)

			return err
		},
		"whitespace speech": func() error {
			_, err := svc.Speak(t.Context(), opaqueRef, "  ", german, nil)

			return err
		},
		"BCP-47 language": func() error {
			_, err := svc.Speak(t.Context(), opaqueRef, "Hallo", "de-DE", nil)

			return err
		},
		"uppercase design language": func() error {
			_, err := svc.Design(t.Context(), description, sampleText, "DE", nil)

			return err
		},
		"description under 20 characters": func() error {
			_, err := svc.Design(t.Context(), "short", sampleText, german, nil)

			return err
		},
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			requireCode(t, call(), connect.CodeInvalidArgument)
		})
	}

	c, err := svc.GetCapabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if c.DescriptionLimits != (voice.TextLimits{MinChars: 20, MaxChars: 1000}) || c.MaxSampleSeconds != 120 ||
		c.SpeechModels[0].MaxTextChars != 5000 {
		t.Fatalf("capabilities = %+v", c)
	}
}

func TestSpeak(t *testing.T) {
	t.Parallel()

	var got rootpullertest.VoiceSpeakRequest

	svc := newService(t, &rootpullertest.Voice{
		SpeakFunc: func(req rootpullertest.VoiceSpeakRequest) (*voice.Speech, error) {
			got = req

			return &voice.Speech{
				Audio:    rootpullersdk.File{MIMEType: mimeMP3, Data: []byte("mp3")},
				Model:    speechModel,
				Duration: 1500 * time.Millisecond,
				Usage:    voice.Usage{Characters: 12},
			}, nil
		},
	})

	speed, stability := 0.75, 0.4

	speech, err := svc.Speak(t.Context(), opaqueRef, "Guten Morgen", german, &voice.SpeakOptions{
		Model: speechModel, Speed: &speed, Stability: &stability,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Ref != opaqueRef || got.Text != "Guten Morgen" || *got.Model != speechModel ||
		math.Abs(*got.Speed-0.75) > 1e-6 || math.Abs(*got.Stability-0.4) > 1e-6 {
		t.Fatalf("request = %+v", got)
	}

	if speech.Audio.Name != "speech_0.mp3" || string(speech.Audio.Data) != "mp3" ||
		speech.Duration != 1500*time.Millisecond || speech.Usage.Characters != 12 {
		t.Fatalf("speech = %+v", speech)
	}
}

func TestSpeak_SettingsOutOfRangeFailLocally(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{})

	for _, v := range []float64{-0.1, 1.1, math.NaN()} {
		_, err := svc.Speak(t.Context(), "r", "t", german, &voice.SpeakOptions{Speed: &v})
		requireCode(t, err, connect.CodeInvalidArgument)
	}
}

func TestProviderMismatch(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{
		SpeakFunc: func(rootpullertest.VoiceSpeakRequest) (*voice.Speech, error) {
			return nil, rootpullertest.VoiceProviderMismatch()
		},
	})

	_, err := svc.Speak(t.Context(), "old-ref", "Hallo", german, nil)
	requireCode(t, err, connect.CodeFailedPrecondition)

	if !voice.IsProviderMismatch(err) {
		t.Fatalf("IsProviderMismatch = false for %v", err)
	}

	if voice.IsProviderMismatch(connect.NewError(connect.CodeFailedPrecondition, errOther)) {
		t.Fatal("a precondition without the reason is not a mismatch")
	}
}

func TestDeleteAndCapabilities(t *testing.T) {
	t.Parallel()

	var deleted voice.Ref

	used, limit := 3, 10

	svc := newService(t, &rootpullertest.Voice{
		DeleteFunc: func(ref voice.Ref) error {
			deleted = ref

			return nil
		},
		Capabilities: &voice.Capabilities{
			CanDesign: true, SlotsUsed: &used, SlotsLimit: &limit, MaxTextChars: 10000,
			SpeechModels: []voice.Model{{ID: speechModel, Languages: []string{"de"}}},
		},
	})

	if err := svc.Delete(t.Context(), opaqueRef); err != nil {
		t.Fatal(err)
	}

	if deleted != opaqueRef {
		t.Fatalf("deleted = %q", deleted)
	}

	c, err := svc.GetCapabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if !c.CanDesign || c.CanClone || *c.SlotsUsed != 3 || *c.SlotsLimit != 10 || c.MaxTextChars != 10000 ||
		len(c.SpeechModels) != 1 || c.SpeechModels[0].Languages[0] != german {
		t.Fatalf("capabilities = %+v", c)
	}
}

func TestSpeak_Format(t *testing.T) {
	t.Parallel()

	var got rootpullertest.VoiceSpeakRequest

	svc := newService(t, &rootpullertest.Voice{
		SpeakFunc: func(req rootpullertest.VoiceSpeakRequest) (*voice.Speech, error) {
			got = req

			return &voice.Speech{Audio: rootpullersdk.File{MIMEType: "audio/wav", Data: []byte("RIFF")}}, nil
		},
	})

	format := voice.AudioFormat{Codec: voice.CodecWAV, SampleRate: 48000, Channels: 2}

	speech, err := svc.Speak(t.Context(), opaqueRef, "Guten Morgen", german, &voice.SpeakOptions{Format: &format})
	if err != nil {
		t.Fatal(err)
	}

	if got.Format == nil || *got.Format != (voice.AudioFormat{Codec: voice.CodecWAV, SampleRate: 48000}) {
		t.Fatalf("requested format = %+v; channels are never sent", got.Format)
	}

	if speech.Audio.Name != "speech_0.wav" || speech.Audio.MIMEType != "audio/wav" ||
		speech.Format != (voice.AudioFormat{Codec: voice.CodecWAV, SampleRate: 48000, Channels: 1}) {
		t.Fatalf("speech = %+v, format %+v", speech.Audio, speech.Format)
	}
}

func TestSpeak_DefaultFormat(t *testing.T) {
	t.Parallel()

	var got rootpullertest.VoiceSpeakRequest

	svc := newService(t, &rootpullertest.Voice{
		SpeakFunc: func(req rootpullertest.VoiceSpeakRequest) (*voice.Speech, error) {
			got = req

			return &voice.Speech{Audio: rootpullersdk.File{MIMEType: mimeMP3, Data: []byte("mp3")}}, nil
		},
	})

	speech, err := svc.Speak(t.Context(), opaqueRef, "Guten Morgen", german, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got.Format != nil {
		t.Fatalf("requested format = %+v, want none", got.Format)
	}

	if speech.Format != (voice.AudioFormat{Codec: voice.CodecMP3, SampleRate: 44100, Bitrate: 128, Channels: 1}) {
		t.Fatalf("format = %+v", speech.Format)
	}
}

func TestSpeak_FormatErrors(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{})

	for name, f := range map[string]voice.AudioFormat{
		"unknown codec": {Codec: "flac", SampleRate: 44100},
		"no rate":       {Codec: voice.CodecMP3, Bitrate: 128},
		"not offered":   {Codec: voice.CodecMP3, SampleRate: 44100, Bitrate: 320},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.Speak(t.Context(), opaqueRef, "Guten Morgen", german, &voice.SpeakOptions{Format: &f})
			requireCode(t, err, connect.CodeInvalidArgument)
		})
	}
}

func TestCapabilities_AudioFormats(t *testing.T) {
	t.Parallel()

	c, err := newService(t, &rootpullertest.Voice{}).GetCapabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(c.AudioFormats) == 0 || c.AudioFormats[0] != (voice.AudioFormat{Codec: voice.CodecMP3, SampleRate: 44100, Bitrate: 128}) {
		t.Fatalf("audio formats = %+v, want the default first", c.AudioFormats)
	}
}
