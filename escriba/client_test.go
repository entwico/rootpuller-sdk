package escriba_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/escriba"
	escribapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba/escribaconnect"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

const (
	wantText  = "Guten Tag."
	wantModel = "small"
)

// errAtCapacity is a static error so the fake can report backpressure.
var errAtCapacity = errors.New("at capacity")

func newService(t *testing.T, fake *rootpullertest.Escriba, opts ...escriba.Option) *escriba.Service {
	t.Helper()

	srv := rootpullertest.NewServer(t, fake)

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return escriba.NewService(sdk, opts...)
}

// assertCode checks the code a consumer actually sees, which rides on the
// SDK's own error type rather than on *connect.Error.
func assertCode(t *testing.T, err error, want connect.Code) {
	t.Helper()

	var sdkErr *rootpullersdk.Error
	if !errors.As(err, &sdkErr) {
		t.Fatalf("got %T (%v), want a *rootpullersdk.Error", err, err)
	}

	if sdkErr.Code != want {
		t.Errorf("got code %v, want %v", sdkErr.Code, want)
	}
}

// collect drains a live session and returns the events in order.
func collect(t *testing.T, session *escriba.LiveSession) []escriba.Event {
	t.Helper()

	events := make([]escriba.Event, 0, 6)

	for event, err := range session.Events(t.Context()) {
		if err != nil {
			t.Fatalf("Events: %v", err)
		}

		events = append(events, event)
	}

	return events
}

func TestOpenLiveDeliversEventsInOrder(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Escriba{})

	session, err := svc.OpenLive(t.Context(), escriba.LiveConfig{SampleRate: 16000, Language: "de"})
	if err != nil {
		t.Fatalf("OpenLive: %v", err)
	}
	defer session.Close()

	if err := session.Send([]byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if err := session.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	events := collect(t, session)

	wantKinds := []escriba.EventKind{
		escriba.EventKindReady,
		escriba.EventKindPartial,
		escriba.EventKindCommitted,
		escriba.EventKindUtteranceEnd,
		escriba.EventKindRevision,
		escriba.EventKindComplete,
	}

	if len(events) != len(wantKinds) {
		t.Fatalf("got %d events, want %d", len(events), len(wantKinds))
	}

	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Errorf("event %d: got kind %q, want %q", i, events[i].Kind, want)
		}
	}

	// A revision must arrive after its utterance closed: that ordering is what
	// lets a consumer replace an already-rendered group.
	if events[4].Revision.Text != wantText {
		t.Errorf("got revision %q, want %q", events[4].Revision.Text, wantText)
	}

	if got := events[0].Ready.Language; got != "de" {
		t.Errorf("got ready language %q, want %q", got, "de")
	}
}

func TestOpenLiveResultIsTheTerminalTranscript(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Escriba{})

	session, err := svc.OpenLive(t.Context(), escriba.LiveConfig{SampleRate: 16000})
	if err != nil {
		t.Fatalf("OpenLive: %v", err)
	}
	defer session.Close()

	if err := session.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	if _, ok := session.Result(); ok {
		t.Fatal("Result available before Events finished")
	}

	collect(t, session)

	result, ok := session.Result()
	if !ok {
		t.Fatal("Result missing after Events finished")
	}

	if result.Text != wantText {
		t.Errorf("got text %q, want %q", result.Text, wantText)
	}

	if result.Reason != escriba.EndReasonClientClosed {
		t.Errorf("got reason %q, want %q", result.Reason, escriba.EndReasonClientClosed)
	}

	if len(result.Utterances) != 1 || !result.Utterances[0].Refined {
		t.Errorf("got utterances %+v, want one refined", result.Utterances)
	}
}

func TestOpenLiveSendsTheConfigTheServerExpects(t *testing.T) {
	t.Parallel()

	refine := false
	gotCfg := make(chan escriba.LiveConfig, 1)
	gotAudio := make(chan [][]byte, 1)

	fake := &rootpullertest.Escriba{
		LiveFunc: func(cfg escriba.LiveConfig, audio [][]byte) (rootpullertest.LiveScript, error) {
			gotCfg <- cfg

			gotAudio <- audio

			return rootpullertest.LiveScript{}, nil
		},
	}

	svc := newService(t, fake, escriba.WithDefaultLanguage("de"), escriba.WithDefaultModel(wantModel))

	session, err := svc.OpenLive(t.Context(), escriba.LiveConfig{
		SampleRate:       16000,
		Encoding:         escriba.EncodingPCMS16LE,
		EnableRefinement: &refine,
	})
	if err != nil {
		t.Fatalf("OpenLive: %v", err)
	}
	defer session.Close()

	if err := session.Send([]byte{9, 9}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Empty frames are not worth a round trip.
	if err := session.Send(nil); err != nil {
		t.Fatalf("Send(nil): %v", err)
	}

	if err := session.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	collect(t, session)

	cfg := <-gotCfg
	if cfg.SampleRate != 16000 {
		t.Errorf("got sample rate %d, want 16000", cfg.SampleRate)
	}

	// Construction defaults fill the fields the call left empty.
	if cfg.Language != "de" {
		t.Errorf("got language %q, want %q", cfg.Language, "de")
	}

	if cfg.Model != wantModel {
		t.Errorf("got model %q, want %q", cfg.Model, wantModel)
	}

	if cfg.EnableRefinement == nil || *cfg.EnableRefinement {
		t.Errorf("got refinement %v, want an explicit false", cfg.EnableRefinement)
	}

	if audio := <-gotAudio; len(audio) != 1 {
		t.Errorf("got %d audio frames, want 1 (empty frames skipped)", len(audio))
	}
}

func TestOpenLiveRequiresASampleRate(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Escriba{})

	_, err := svc.OpenLive(t.Context(), escriba.LiveConfig{})
	if err == nil {
		t.Fatal("expected an error")
	}

	assertCode(t, err, connect.CodeInvalidArgument)
}

func TestOpenLiveSurfacesServerErrors(t *testing.T) {
	t.Parallel()

	fake := &rootpullertest.Escriba{
		LiveFunc: func(escriba.LiveConfig, [][]byte) (rootpullertest.LiveScript, error) {
			return rootpullertest.LiveScript{},
				connect.NewError(connect.CodeResourceExhausted, errAtCapacity)
		},
	}

	svc := newService(t, fake)

	session, err := svc.OpenLive(t.Context(), escriba.LiveConfig{SampleRate: 16000})
	if err != nil {
		t.Fatalf("OpenLive: %v", err)
	}
	defer session.Close()

	if err := session.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	var iterErr error

	for _, err := range session.Events(t.Context()) {
		if err != nil {
			iterErr = err

			break
		}
	}

	if iterErr == nil {
		t.Fatal("expected the server error to surface through Events")
	}

	assertCode(t, iterErr, connect.CodeResourceExhausted)
}

func TestEventsRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	// A session the server never finalises must still release the consumer.
	fake := &rootpullertest.Escriba{
		LiveFunc: func(escriba.LiveConfig, [][]byte) (rootpullertest.LiveScript, error) {
			time.Sleep(2 * time.Second)

			return rootpullertest.LiveScript{}, nil
		},
	}

	svc := newService(t, fake)

	session, err := svc.OpenLive(t.Context(), escriba.LiveConfig{SampleRate: 16000})
	if err != nil {
		t.Fatalf("OpenLive: %v", err)
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	var iterErr error

	for _, err := range session.Events(ctx) {
		if err != nil {
			iterErr = err

			break
		}
	}

	if iterErr == nil {
		t.Fatal("expected a cancellation error")
	}

	assertCode(t, iterErr, connect.CodeCanceled)
}

func TestSessionLimitIsACleanEnd(t *testing.T) {
	t.Parallel()

	fake := &rootpullertest.Escriba{
		LiveFunc: func(escriba.LiveConfig, [][]byte) (rootpullertest.LiveScript, error) {
			return rootpullertest.LiveScript{
				Complete: escriba.Complete{Text: "so weit", Reason: escriba.EndReasonSessionLimit},
			}, nil
		},
	}

	svc := newService(t, fake)

	session, err := svc.OpenLive(t.Context(), escriba.LiveConfig{SampleRate: 16000})
	if err != nil {
		t.Fatalf("OpenLive: %v", err)
	}
	defer session.Close()

	if err := session.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	collect(t, session)

	result, ok := session.Result()
	if !ok {
		t.Fatal("Result missing")
	}

	// Not an error: the transcript so far is complete and valid.
	if result.Reason != escriba.EndReasonSessionLimit {
		t.Errorf("got reason %q, want %q", result.Reason, escriba.EndReasonSessionLimit)
	}
}

func TestTranscribe(t *testing.T) {
	t.Parallel()

	gotOpts := make(chan escriba.TranscribeOptions, 1)
	gotAudio := make(chan []byte, 1)

	fake := &rootpullertest.Escriba{
		TranscribeFunc: func(opts escriba.TranscribeOptions, audio []byte) (escriba.Transcript, error) {
			gotOpts <- opts

			gotAudio <- audio

			return escriba.Transcript{
				Text:     wantText,
				Duration: 2500 * time.Millisecond,
				Model:    wantModel,
				Language: escriba.DetectedLanguage{Language: "de", Probability: 0.98, Detected: true},
				Words:    []escriba.Word{{Start: 100 * time.Millisecond, End: 600 * time.Millisecond, Text: "Guten"}},
			}, nil
		},
	}

	svc := newService(t, fake)

	transcript, err := svc.Transcribe(t.Context(),
		rootpullersdk.UploadBytes("call.wav", "audio/wav", []byte("RIFFdata")),
		&escriba.TranscribeOptions{Language: "de", IncludeWords: true})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if opts := <-gotOpts; opts.Language != "de" || !opts.IncludeWords {
		t.Errorf("got opts %+v, want language de with words", opts)
	}

	if audio := <-gotAudio; string(audio) != "RIFFdata" {
		t.Errorf("got audio %q, want %q", audio, "RIFFdata")
	}

	if transcript.Text != wantText {
		t.Errorf("got text %q, want %q", transcript.Text, wantText)
	}

	if transcript.Duration != 2500*time.Millisecond {
		t.Errorf("got duration %v, want 2.5s", transcript.Duration)
	}

	if !transcript.Language.Detected || transcript.Language.Language != "de" {
		t.Errorf("got language %+v, want detected de", transcript.Language)
	}

	if len(transcript.Words) != 1 || transcript.Words[0].Text != "Guten" {
		t.Errorf("got words %+v, want one word", transcript.Words)
	}

	if transcript.Model != wantModel {
		t.Errorf("got model %q, want %q", transcript.Model, wantModel)
	}
}

func TestTranscribeAppliesConstructionDefaults(t *testing.T) {
	t.Parallel()

	gotOpts := make(chan escriba.TranscribeOptions, 1)

	fake := &rootpullertest.Escriba{
		TranscribeFunc: func(opts escriba.TranscribeOptions, _ []byte) (escriba.Transcript, error) {
			gotOpts <- opts

			return escriba.Transcript{}, nil
		},
	}

	svc := newService(t, fake, escriba.WithDefaultLanguage("es"), escriba.WithDefaultModel("large-v3"))

	if _, err := svc.Transcribe(t.Context(),
		rootpullersdk.UploadBytes("audio.wav", "audio/wav", []byte("audio")), nil); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	opts := <-gotOpts
	if opts.Language != "es" || opts.Model != "large-v3" {
		t.Errorf("got opts %+v, want the construction defaults", opts)
	}
}

func TestGetCapabilities(t *testing.T) {
	t.Parallel()

	fake := &rootpullertest.Escriba{
		Capabilities: escriba.Capabilities{
			Models: []escriba.ModelInfo{
				{Model: wantModel, Multilingual: true, Loaded: true, Description: "Whisper small"},
			},
			DefaultLanguage:   "de",
			MaxSession:        30 * time.Minute,
			MaxRecording:      2 * time.Minute,
			RefinementEnabled: true,
		},
	}

	caps, err := newService(t, fake).GetCapabilities(t.Context())
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}

	if len(caps.Models) != 1 || caps.Models[0].Model != wantModel {
		t.Fatalf("got models %+v, want one small model", caps.Models)
	}

	if caps.MaxSession != 30*time.Minute {
		t.Errorf("got max session %v, want 30m", caps.MaxSession)
	}

	if caps.MaxRecording != 2*time.Minute {
		t.Errorf("got max recording %v, want 2m", caps.MaxRecording)
	}

	if !caps.RefinementEnabled {
		t.Error("got refinement disabled, want enabled")
	}
}

// captureDeployment records the escriba-deployment header the SDK sent, using a
// raw mux so the assertion is on the wire value rather than on a fake's view.
func captureDeployment(t *testing.T, seen chan<- string) *escriba.Service {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(escribaconnect.NewTranscriptionServiceHandler(&escribaHandlerStub{seen: seen}))

	srv := rootpullertest.NewServerWithMux(t, mux)

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return escriba.NewService(sdk, escriba.WithDeployment("cpu"))
}

type escribaHandlerStub struct {
	escribaconnect.UnimplementedTranscriptionServiceHandler

	seen chan<- string
}

func (h *escribaHandlerStub) GetCapabilities(
	_ context.Context,
	req *connect.Request[emptypb.Empty],
) (*connect.Response[escribapb.Capabilities], error) {
	h.seen <- req.Header().Get("Escriba-Deployment")

	return connect.NewResponse(&escribapb.Capabilities{}), nil
}

func (h *escribaHandlerStub) TranscribeLive(
	_ context.Context,
	stream *connect.BidiStream[escribapb.TranscribeLiveRequest, escribapb.TranscribeLiveResponse],
) error {
	h.seen <- stream.RequestHeader().Get("Escriba-Deployment")

	return stream.Send(&escribapb.TranscribeLiveResponse{
		Event: &escribapb.TranscribeLiveResponse_Complete{
			Complete: &escribapb.TranscriptComplete{Text: "done"},
		},
	})
}

func TestDeploymentHeaderIsSentOnUnaryCalls(t *testing.T) {
	t.Parallel()

	seen := make(chan string, 1)

	if _, err := captureDeployment(t, seen).GetCapabilities(t.Context()); err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}

	if got := <-seen; got != "cpu" {
		t.Errorf("got deployment header %q, want %q", got, "cpu")
	}
}

func TestDeploymentHeaderIsSentOnLiveStreams(t *testing.T) {
	t.Parallel()

	// The streaming path sets headers through a different interceptor hook, so
	// it is worth asserting separately — a live session is the main reason to
	// pick a deployment at all.
	seen := make(chan string, 1)

	session, err := captureDeployment(t, seen).OpenLive(t.Context(), escriba.LiveConfig{SampleRate: 16000})
	if err != nil {
		t.Fatalf("OpenLive: %v", err)
	}
	defer session.Close()

	if got := <-seen; got != "cpu" {
		t.Errorf("got deployment header %q, want %q", got, "cpu")
	}
}

func TestPerCallDeploymentOverridesTheDefault(t *testing.T) {
	t.Parallel()

	// The comparison workflow: one client, same audio, different workers.
	seen := make(chan string, 1)
	svc := captureDeployment(t, seen)

	ctx := rootpullersdk.ContextWithEscribaDeployment(t.Context(), "gpu")
	if _, err := svc.GetCapabilities(ctx); err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}

	if got := <-seen; got != "gpu" {
		t.Errorf("got deployment header %q, want the per-call %q", got, "gpu")
	}
}
