package voice_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/voice"
)

const (
	mimeWAV    = "audio/wav"
	sampleName = "me.wav"
)

func sampleUpload(data []byte) *rootpullersdk.Upload {
	return &rootpullersdk.Upload{Name: sampleName, MIMEType: mimeWAV, Size: int64(len(data)), Content: bytes.NewReader(data)}
}

func TestCloneSpeak(t *testing.T) {
	t.Parallel()

	var got rootpullertest.VoiceCloneSpeakRequest

	svc := newService(t, &rootpullertest.Voice{
		CloneSpeakFunc: func(req rootpullertest.VoiceCloneSpeakRequest) (*voice.Speech, error) {
			got = req

			return &voice.Speech{
				Audio: rootpullersdk.File{MIMEType: mimeWAV, Data: []byte("RIFF")},
				Model: speechModel,
				Usage: voice.Usage{Characters: 5},
			}, nil
		},
	})

	// larger than one chunk, so the sample arrives in several frames.
	sample := bytes.Repeat([]byte{7}, 3<<20)
	stability := 0.6

	speech, err := svc.CloneSpeak(t.Context(), sampleUpload(sample), "Hallo", german, &voice.CloneSpeakOptions{
		Provider:   voice.ProviderFishAudio,
		SampleText: "so klinge ich",
		SpeakOptions: voice.SpeakOptions{
			Stability: &stability,
			Format:    &voice.AudioFormat{Codec: voice.CodecWAV, SampleRate: 48000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Provider != voice.ProviderFishAudio || got.Text != "Hallo" || got.Language != german ||
		got.SampleText == nil || *got.SampleText != "so klinge ich" ||
		got.Stability == nil || math.Abs(*got.Stability-0.6) > 1e-6 || got.Speed != nil {
		t.Fatalf("request = %+v", got)
	}

	if got.Sample == nil || got.Sample.Name != sampleName || !bytes.Equal(got.Sample.Data, sample) {
		t.Fatal("the sample did not arrive whole")
	}

	if got.Format == nil || *got.Format != (voice.AudioFormat{Codec: voice.CodecWAV, SampleRate: 48000}) {
		t.Fatalf("format = %+v", got.Format)
	}

	if speech.Audio.Name != "speech_0.wav" || speech.Model != speechModel || speech.Format.Codec != voice.CodecWAV {
		t.Fatalf("speech = %+v", speech)
	}
}

func TestCloneSpeak_Canned(t *testing.T) {
	t.Parallel()

	speech, err := newService(t, &rootpullertest.Voice{}).CloneSpeak(t.Context(), sampleUpload([]byte("abc")), "Hallo", german, nil)
	if err != nil {
		t.Fatal(err)
	}

	if speech.Audio.Name != "speech_0.mp3" || len(speech.Audio.Data) == 0 || speech.Format.Channels != 1 {
		t.Fatalf("speech = %+v", speech)
	}
}

func TestCloneSpeak_Errors(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{})
	tooFast := 1.5

	// local is set for checks the SDK makes before uploading anything: its own
	// messages start with "voice: ", the fake's never do, so a check that
	// moved to the server would fail here instead of passing on the fake's
	// rejection.
	cases := map[string]struct {
		call  func() error
		local bool
	}{
		"no sample": {local: true, call: func() error {
			_, err := svc.CloneSpeak(t.Context(), nil, "Hallo", german, nil)

			return err
		}},
		"sample without content": {local: true, call: func() error {
			_, err := svc.CloneSpeak(t.Context(), &rootpullersdk.Upload{Name: sampleName}, "Hallo", german, nil)

			return err
		}},
		"empty sample": {call: func() error {
			_, err := svc.CloneSpeak(t.Context(), sampleUpload(nil), "Hallo", german, nil)

			return err
		}},
		"no text": {call: func() error {
			_, err := svc.CloneSpeak(t.Context(), sampleUpload([]byte("a")), " ", german, nil)

			return err
		}},
		"speed out of range": {local: true, call: func() error {
			_, err := svc.CloneSpeak(t.Context(), sampleUpload([]byte("a")), "Hallo", german,
				&voice.CloneSpeakOptions{SpeakOptions: voice.SpeakOptions{Speed: &tooFast}})

			return err
		}},
		"unknown provider": {local: true, call: func() error {
			_, err := svc.CloneSpeak(t.Context(), sampleUpload([]byte("a")), "Hallo", german,
				&voice.CloneSpeakOptions{Provider: "acme"})

			return err
		}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.call()
			requireCode(t, err, connect.CodeInvalidArgument)

			if tc.local && !strings.Contains(err.Error(), "voice: ") {
				t.Fatalf("want the SDK's local check, got the server's: %v", err)
			}
		})
	}
}

func TestSave_SampleWithoutContentFailsLocally(t *testing.T) {
	t.Parallel()

	_, err := newService(t, &rootpullertest.Voice{}).Save(t.Context(), voiceName,
		&voice.SaveOptions{Sample: &rootpullersdk.Upload{Name: sampleName}})
	requireCode(t, err, connect.CodeInvalidArgument)
}

func TestSave_HandleIgnoresProviderLikeTheServer(t *testing.T) {
	t.Parallel()

	var got rootpullertest.VoiceSaveRequest

	svc := newService(t, &rootpullertest.Voice{
		Capabilities: &voice.Capabilities{
			AvailableProviders: []voice.Provider{voice.ProviderElevenLabs},
			AudioFormats:       []voice.AudioFormat{{Codec: voice.CodecMP3, SampleRate: 44100, Bitrate: 128}},
		},
		SaveFunc: func(req rootpullertest.VoiceSaveRequest) (*voice.SaveResult, error) {
			got = req

			return &voice.SaveResult{Ref: opaqueRef}, nil
		},
	})

	// a handle saves at its own provider, so an unconfigured clone target
	// does not matter until the sample has to be cloned.
	_, err := svc.Save(t.Context(), voiceName, &voice.SaveOptions{
		Provider: voice.ProviderFishAudio, Handle: "h", Sample: sampleUpload([]byte("a")),
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Provider != voice.ProviderFishAudio {
		t.Fatalf("recorded provider = %q, want what was requested", got.Provider)
	}

	_, err = svc.Save(t.Context(), voiceName, &voice.SaveOptions{
		Provider: voice.ProviderFishAudio, Sample: sampleUpload([]byte("a")),
	})
	requireCode(t, err, connect.CodeUnavailable)
}

func TestProviders(t *testing.T) {
	t.Parallel()

	var (
		design rootpullertest.VoiceDesignRequest
		save   rootpullertest.VoiceSaveRequest
	)

	svc := newService(t, &rootpullertest.Voice{
		DesignFunc: func(req rootpullertest.VoiceDesignRequest) (*voice.DesignResult, error) {
			design = req

			return &voice.DesignResult{Previews: []voice.Preview{{Handle: "h", Audio: rootpullersdk.File{Data: []byte{1}}}}}, nil
		},
		SaveFunc: func(req rootpullertest.VoiceSaveRequest) (*voice.SaveResult, error) {
			save = req

			return &voice.SaveResult{Ref: opaqueRef, Cloned: true}, nil
		},
	})

	if _, err := svc.Design(t.Context(), description, sampleText, german, &voice.DesignOptions{Provider: voice.ProviderFishAudio}); err != nil {
		t.Fatal(err)
	}

	if design.Provider != voice.ProviderFishAudio {
		t.Fatalf("design provider = %q", design.Provider)
	}

	if _, err := svc.Save(t.Context(), voiceName, &voice.SaveOptions{Provider: voice.ProviderFishAudio, Sample: sampleUpload([]byte("a"))}); err != nil {
		t.Fatal(err)
	}

	if save.Provider != voice.ProviderFishAudio {
		t.Fatalf("save provider = %q", save.Provider)
	}

	if _, err := svc.Design(t.Context(), description, sampleText, german, nil); err != nil {
		t.Fatal(err)
	}

	if design.Provider != voice.ProviderDefault {
		t.Fatalf("default provider = %q, want the request's unset provider", design.Provider)
	}

	c, err := svc.GetCapabilities(t.Context(), voice.ProviderFishAudio)
	if err != nil {
		t.Fatal(err)
	}

	if c.Provider != voice.ProviderFishAudio || !c.CanCloneSpeak ||
		len(c.AvailableProviders) != 2 || c.AvailableProviders[0] != voice.ProviderElevenLabs {
		t.Fatalf("capabilities = %+v", c)
	}
}

func TestProviders_NotConfigured(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Voice{Capabilities: &voice.Capabilities{
		AvailableProviders: []voice.Provider{voice.ProviderElevenLabs},
		AudioFormats:       []voice.AudioFormat{{Codec: voice.CodecMP3, SampleRate: 44100, Bitrate: 128}},
	}})

	_, err := svc.GetCapabilities(t.Context(), voice.ProviderFishAudio)
	requireCode(t, err, connect.CodeUnavailable)

	_, err = svc.GetCapabilities(t.Context(), "acme")
	requireCode(t, err, connect.CodeInvalidArgument)
}
