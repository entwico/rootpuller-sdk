package escriba_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/escriba"
	escribapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba/escribaconnect"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

// errQueueFull is a static error so the fake can refuse a recording.
var errQueueFull = errors.New("recording queue is full")

func recordingUpload(data []byte) rootpullersdk.Upload {
	return rootpullersdk.UploadBytes("call.m4a", "audio/mp4", data)
}

func TestTranscribeRecordingReturnsTheWholeTranscript(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Escriba{})

	recording, err := svc.TranscribeRecording(t.Context(), recordingUpload([]byte("audio")), nil)
	if err != nil {
		t.Fatalf("TranscribeRecording: %v", err)
	}

	if recording.Text != "Praxis Dr. Weber. Guten Tag. Ich brauche einen Termin." {
		t.Errorf("got text %q", recording.Text)
	}

	if recording.Duration != 6*time.Second || recording.Model != wantModel {
		t.Errorf("got duration %v model %q, want 6s %q", recording.Duration, recording.Model, wantModel)
	}

	if !recording.Language.Detected || recording.Language.Language != "de" {
		t.Errorf("got language %+v, want detected de", recording.Language)
	}

	if len(recording.Segments) != 3 {
		t.Fatalf("got %d segments, want 3", len(recording.Segments))
	}

	first := recording.Segments[0]
	if first.Speaker == nil || *first.Speaker != 0 {
		t.Errorf("got speaker %v on the first segment, want 0: speaker zero must not read as absent", first.Speaker)
	}

	if last := recording.Segments[2]; last.Start != 4*time.Second || last.End != 6*time.Second || *last.Speaker != 1 {
		t.Errorf("got last segment %+v, want 4s-6s by speaker 1", last)
	}

	if len(recording.Speakers) != 2 || recording.Speakers[0].SpeakingTime != 3*time.Second {
		t.Errorf("got speakers %+v, want two with 3s for the first", recording.Speakers)
	}
}

func TestTranscribeRecordingReportsProgressAndSegmentsAsTheyArrive(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Escriba{})

	var (
		stages   []escriba.RecordingStage
		queuedAt int
		texts    []string
	)

	_, err := svc.TranscribeRecording(t.Context(), recordingUpload([]byte("audio")), &escriba.RecordingOptions{
		OnProgress: func(progress escriba.RecordingProgress) {
			stages = append(stages, progress.Stage)

			if progress.Stage == escriba.RecordingStageQueued {
				queuedAt = progress.QueuePosition
			}
		},
		OnSegment: func(segment escriba.Segment) { texts = append(texts, segment.Text) },
	})
	if err != nil {
		t.Fatalf("TranscribeRecording: %v", err)
	}

	want := []escriba.RecordingStage{
		escriba.RecordingStageQueued, escriba.RecordingStageTranscribing, escriba.RecordingStageLabelingSpeakers,
	}
	if len(stages) != len(want) || stages[0] != want[0] || stages[1] != want[1] || stages[2] != want[2] {
		t.Errorf("got stages %v, want %v", stages, want)
	}

	if queuedAt != 1 {
		t.Errorf("got queue position %d, want 1", queuedAt)
	}

	if len(texts) != 3 || texts[0] != "Praxis Dr. Weber." {
		t.Errorf("got segment texts %v", texts)
	}
}

func TestTranscribeRecordingSendsTheOptionsAndTheWholeUpload(t *testing.T) {
	t.Parallel()

	// Larger than one chunk frame, so reassembly is actually exercised.
	payload := bytes.Repeat([]byte("0123456789abcdef"), 200_000)

	gotOpts := make(chan escriba.RecordingOptions, 1)
	gotAudio := make(chan []byte, 1)

	fake := &rootpullertest.Escriba{
		RecordingFunc: func(opts escriba.RecordingOptions, audio []byte) (rootpullertest.RecordingScript, error) {
			gotOpts <- opts

			gotAudio <- audio

			return rootpullertest.RecordingScript{}, nil
		},
	}

	svc := newService(t, fake, escriba.WithDefaultLanguage("de"), escriba.WithDefaultModel("large-v3-turbo"))

	_, err := svc.TranscribeRecording(t.Context(), recordingUpload(payload), &escriba.RecordingOptions{
		IncludeWords: true,
		Speakers:     &escriba.SpeakerOptions{Min: 2, Max: 5},
	})
	if err != nil {
		t.Fatalf("TranscribeRecording: %v", err)
	}

	opts := <-gotOpts
	if opts.Language != "de" || opts.Model != "large-v3-turbo" || !opts.IncludeWords {
		t.Errorf("got opts %+v, want the construction defaults and words", opts)
	}

	if opts.Speakers == nil || opts.Speakers.Method != escriba.SpeakerMethodDiarization ||
		opts.Speakers.Min != 2 || opts.Speakers.Max != 5 {
		t.Errorf("got speakers %+v, want diarization bounded 2-5", opts.Speakers)
	}

	if audio := <-gotAudio; !bytes.Equal(audio, payload) {
		t.Errorf("got %d bytes of audio, want the %d sent, intact", len(audio), len(payload))
	}
}

func TestTranscribeRecordingWithoutSpeakersAsksForNone(t *testing.T) {
	t.Parallel()

	gotOpts := make(chan escriba.RecordingOptions, 1)

	fake := &rootpullertest.Escriba{
		RecordingFunc: func(opts escriba.RecordingOptions, _ []byte) (rootpullertest.RecordingScript, error) {
			gotOpts <- opts

			return rootpullertest.RecordingScript{
				Recording: escriba.Recording{Segments: []escriba.Segment{{Text: "ohne Sprecher"}}},
			}, nil
		},
	}

	recording, err := newService(t, fake).TranscribeRecording(t.Context(), recordingUpload([]byte("a")), nil)
	if err != nil {
		t.Fatalf("TranscribeRecording: %v", err)
	}

	if opts := <-gotOpts; opts.Speakers != nil {
		t.Errorf("got speakers %+v, want none requested", opts.Speakers)
	}

	if recording.Segments[0].Speaker != nil {
		t.Errorf("got speaker %v, want an unlabelled segment", *recording.Segments[0].Speaker)
	}
}

func TestTranscribeRecordingRejectsContradictoryOptionsLocally(t *testing.T) {
	t.Parallel()

	tests := map[string]escriba.SpeakerOptions{
		"negative count":      {Count: -1},
		"count with bounds":   {Count: 2, Max: 4},
		"min above max":       {Min: 5, Max: 2},
		"counts with channel": {Method: escriba.SpeakerMethodChannel, Count: 2},
		"unknown method":      {Method: "telepathy"},
	}

	for name, speakers := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := &rootpullertest.Escriba{
				RecordingFunc: func(escriba.RecordingOptions, []byte) (rootpullertest.RecordingScript, error) {
					t.Error("the server must not be contacted")

					return rootpullertest.RecordingScript{}, nil
				},
			}

			_, err := newService(t, fake).TranscribeRecording(t.Context(), recordingUpload([]byte("a")),
				&escriba.RecordingOptions{Speakers: &speakers})
			assertCode(t, err, connect.CodeInvalidArgument)
		})
	}
}

func TestTranscribeRecordingSurfacesServerErrors(t *testing.T) {
	t.Parallel()

	fake := &rootpullertest.Escriba{
		RecordingFunc: func(escriba.RecordingOptions, []byte) (rootpullertest.RecordingScript, error) {
			return rootpullertest.RecordingScript{}, connect.NewError(connect.CodeResourceExhausted, errQueueFull)
		},
	}

	_, err := newService(t, fake).TranscribeRecording(t.Context(), recordingUpload([]byte("a")), nil)
	assertCode(t, err, connect.CodeResourceExhausted)
}

// truncatedRecording ends the stream cleanly, but without the final event.
type truncatedRecording struct {
	escribaconnect.UnimplementedTranscriptionServiceHandler

	complete *escribapb.RecordingComplete
}

func (h *truncatedRecording) TranscribeRecording(
	_ context.Context,
	stream *connect.BidiStream[escribapb.TranscribeRecordingRequest, escribapb.TranscribeRecordingResponse],
) error {
	for {
		if _, err := stream.Receive(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}
	}

	if err := stream.Send(&escribapb.TranscribeRecordingResponse{
		Event: &escribapb.TranscribeRecordingResponse_Segment{
			Segment: &escribapb.TranscriptSegment{Text: "nur der Anfang"},
		},
	}); err != nil {
		return err
	}

	if h.complete == nil {
		return nil
	}

	return stream.Send(&escribapb.TranscribeRecordingResponse{
		Event: &escribapb.TranscribeRecordingResponse_Complete{Complete: h.complete},
	})
}

func newRawService(t *testing.T, handler escribaconnect.TranscriptionServiceHandler) *escriba.Service {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(escribaconnect.NewTranscriptionServiceHandler(handler))

	sdk, err := rootpullersdk.New(rootpullertest.NewServerWithMux(t, mux).URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return escriba.NewService(sdk)
}

func TestTranscribeRecordingATruncatedStreamIsNotATranscript(t *testing.T) {
	t.Parallel()

	svc := newRawService(t, &truncatedRecording{})

	recording, err := svc.TranscribeRecording(t.Context(), recordingUpload([]byte("a")), nil)
	if recording != nil {
		t.Errorf("got a recording with %d segments, want none handed over", len(recording.Segments))
	}

	assertCode(t, err, connect.CodeInternal)
}

func TestTranscribeRecordingAMissingSegmentIsDataLoss(t *testing.T) {
	t.Parallel()

	svc := newRawService(t, &truncatedRecording{complete: &escribapb.RecordingComplete{SegmentCount: 2}})

	_, err := svc.TranscribeRecording(t.Context(), recordingUpload([]byte("a")), nil)
	assertCode(t, err, connect.CodeDataLoss)
}

func TestTurnsMergeEachSpeakersRun(t *testing.T) {
	t.Parallel()

	recording, err := newService(t, &rootpullertest.Escriba{}).
		TranscribeRecording(t.Context(), recordingUpload([]byte("a")), nil)
	if err != nil {
		t.Fatalf("TranscribeRecording: %v", err)
	}

	turns := recording.Turns()
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(turns))
	}

	if turns[0].Text != "Praxis Dr. Weber. Guten Tag." || turns[0].End != 3*time.Second || *turns[0].Speaker != 0 {
		t.Errorf("got first turn %+v, want the first speaker's two segments merged", turns[0])
	}

	if *turns[1].Speaker != 1 || turns[1].Start != 4*time.Second {
		t.Errorf("got second turn %+v, want speaker 1 from 4s", turns[1])
	}
}

func TestTurnsOfAnUnlabelledRecordingAreOne(t *testing.T) {
	t.Parallel()

	recording := escriba.Recording{Segments: []escriba.Segment{
		{Text: "Erster Satz.", End: time.Second},
		{Text: "Zweiter Satz.", Start: time.Second, End: 2 * time.Second},
	}}

	turns := recording.Turns()
	if len(turns) != 1 || turns[0].Text != "Erster Satz. Zweiter Satz." || turns[0].Speaker != nil {
		t.Errorf("got turns %+v, want a single unlabelled one", turns)
	}
}

func TestGetCapabilitiesReportsRecordingLimits(t *testing.T) {
	t.Parallel()

	fake := &rootpullertest.Escriba{Capabilities: escriba.Capabilities{
		MaxLongRecording:  3 * time.Hour,
		MaxRecordingBytes: 2 << 30,
		SpeakerMethods:    []escriba.SpeakerMethod{escriba.SpeakerMethodDiarization, escriba.SpeakerMethodChannel},
	}}

	caps, err := newService(t, fake).GetCapabilities(t.Context())
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}

	if caps.MaxLongRecording != 3*time.Hour || caps.MaxRecordingBytes != 2<<30 {
		t.Errorf("got limits %v / %d", caps.MaxLongRecording, caps.MaxRecordingBytes)
	}

	if len(caps.SpeakerMethods) != 2 || caps.SpeakerMethods[1] != escriba.SpeakerMethodChannel {
		t.Errorf("got speaker methods %v", caps.SpeakerMethods)
	}
}
