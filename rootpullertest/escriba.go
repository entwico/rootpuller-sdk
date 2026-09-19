package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/entwico/rootpuller-sdk/escriba"
	escribapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba/escribaconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
)

// LiveScript is what a fake live session sends back, in order. The fake always
// prepends a ready event and appends the transcript, so a script only describes
// what happens in between.
type LiveScript struct {
	// Events are sent as soon as the session opens, before any audio arrives —
	// enough for a consumer to exercise ordering without timing games.
	Events []escriba.Event
	// Complete is the terminal event. Zero value yields a usable default.
	Complete escriba.Complete
}

// RecordingScript is what a fake recording sends back once the upload is in.
// The fake frames it the way the server does: accepted first, then Progress in
// order, then one segment event per Recording.Segments entry, then complete —
// so a script only says what the transcript is.
type RecordingScript struct {
	Progress  []escriba.RecordingProgress
	Recording escriba.Recording
}

// Escriba is a fake TranscriptionService.
//
// Nil hooks return a canned session (ready, partial, committed, utterance end,
// revision, complete) so a consumer can exercise revision handling without
// writing a script, and a canned two-speaker recording likewise.
type Escriba struct {
	// LiveFunc returns the script for one live session. It receives the config
	// the client sent and the audio it streamed before half-closing.
	LiveFunc func(cfg escriba.LiveConfig, audio [][]byte) (LiveScript, error)

	// TranscribeFunc answers a one-shot transcription.
	TranscribeFunc func(opts escriba.TranscribeOptions, audio []byte) (escriba.Transcript, error)

	// RecordingFunc returns the script for one recording. It receives the
	// options the client sent (callbacks excluded) and the reassembled upload.
	RecordingFunc func(opts escriba.RecordingOptions, audio []byte) (RecordingScript, error)

	// Capabilities is returned by GetCapabilities.
	Capabilities escriba.Capabilities
}

func (f *Escriba) register(mux *http.ServeMux) {
	mux.Handle(escribaconnect.NewTranscriptionServiceHandler(&escribaHandler{fake: f}))
}

type escribaHandler struct {
	escribaconnect.UnimplementedTranscriptionServiceHandler

	fake *Escriba
}

// TranscribeLive enforces the wire protocol strictly: config first, exactly
// once, before any audio.
func (h *escribaHandler) TranscribeLive(
	_ context.Context,
	stream *connect.BidiStream[escribapb.TranscribeLiveRequest, escribapb.TranscribeLiveResponse],
) error {
	var (
		cfg   *escribapb.TranscribeLiveConfig
		audio [][]byte
	)

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		switch frame := req.GetFrame().(type) {
		case *escribapb.TranscribeLiveRequest_Config:
			if cfg != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			cfg = frame.Config

		case *escribapb.TranscribeLiveRequest_Audio:
			if cfg == nil {
				return errBeforeParams("audio")
			}

			audio = append(audio, frame.Audio.GetData())

		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if cfg == nil {
		return invalidArgument(errMissingParams)
	}

	script, err := h.script(cfg, audio)
	if err != nil {
		return err
	}

	if err := stream.Send(readyResponse(cfg)); err != nil {
		return err
	}

	for _, event := range script.Events {
		resp, ok := liveResponseFromEvent(event)
		if !ok {
			continue
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}

	return stream.Send(completeResponse(script.Complete))
}

// cannedScript exercises the full event vocabulary, including a revision that
// supersedes committed text.
func cannedScript() LiveScript {
	return LiveScript{
		Events: []escriba.Event{
			{Kind: escriba.EventKindPartial, Partial: &escriba.Partial{Text: "guten"}},
			{Kind: escriba.EventKindCommitted, Committed: &escriba.Committed{Text: "guten tag"}},
			{Kind: escriba.EventKindUtteranceEnd, UtteranceEnd: &escriba.UtteranceEnd{}},
			{Kind: escriba.EventKindRevision, Revision: &escriba.Revision{Text: "Guten Tag."}},
		},
		Complete: escriba.Complete{
			Text:       "Guten Tag.",
			Language:   "de",
			Reason:     escriba.EndReasonClientClosed,
			Utterances: []escriba.Utterance{{Text: "Guten Tag.", Refined: true}},
		},
	}
}

// Transcribe enforces config-first and reassembles the chunks.
func (h *escribaHandler) Transcribe(
	_ context.Context,
	stream *connect.ClientStream[escribapb.TranscribeRequest],
) (*connect.Response[escribapb.TranscribeResponse], error) {
	var (
		cfg   *escribapb.TranscribeConfig
		audio []byte
	)

	for stream.Receive() {
		switch frame := stream.Msg().GetFrame().(type) {
		case *escribapb.TranscribeRequest_Config:
			if cfg != nil {
				return nil, invalidArgument(errDuplicateParamsFrame)
			}

			cfg = frame.Config

		case *escribapb.TranscribeRequest_Chunk:
			if cfg == nil {
				return nil, errBeforeParams("chunk")
			}

			if len(frame.Chunk.GetData()) > streamio.MaxChunkBytes {
				return nil, invalidArgument(errChunkTooLarge)
			}

			audio = append(audio, frame.Chunk.GetData()...)

		default:
			return nil, invalidArgument(errUnexpectedVariant)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	if cfg == nil {
		return nil, invalidArgument(errMissingParams)
	}

	transcript := escriba.Transcript{Text: "Guten Tag.", Model: "small"}

	if h.fake.TranscribeFunc != nil {
		opts := escriba.TranscribeOptions{
			Language:     cfg.GetLanguage(),
			Model:        cfg.GetModel().GetModelId(),
			IncludeWords: cfg.GetIncludeWords(),
		}

		got, err := h.fake.TranscribeFunc(opts, audio)
		if err != nil {
			return nil, err
		}

		transcript = got
	}

	return connect.NewResponse(transcribeResponse(transcript)), nil
}

// TranscribeRecording enforces config-first, reassembles the chunks, and only
// answers after the half-close, as the server does.
func (h *escribaHandler) TranscribeRecording(
	_ context.Context,
	stream *connect.BidiStream[escribapb.TranscribeRecordingRequest, escribapb.TranscribeRecordingResponse],
) error {
	var (
		cfg   *escribapb.TranscribeRecordingConfig
		audio []byte
	)

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		switch frame := req.GetFrame().(type) {
		case *escribapb.TranscribeRecordingRequest_Config:
			if cfg != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			cfg = frame.Config

		case *escribapb.TranscribeRecordingRequest_Chunk:
			if cfg == nil {
				return errBeforeParams("chunk")
			}

			if len(frame.Chunk.GetData()) > streamio.MaxChunkBytes {
				return invalidArgument(errChunkTooLarge)
			}

			audio = append(audio, frame.Chunk.GetData()...)

		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if cfg == nil {
		return invalidArgument(errMissingParams)
	}

	script := cannedRecording()

	if h.fake.RecordingFunc != nil {
		got, err := h.fake.RecordingFunc(recordingOptionsFromProto(cfg), audio)
		if err != nil {
			return err
		}

		script = got
	}

	for _, resp := range recordingResponses(script) {
		if err := stream.Send(resp); err != nil {
			return err
		}
	}

	return nil
}

// cannedRecording is a two-speaker call, enough to exercise turn handling.
func cannedRecording() RecordingScript {
	first, second := 0, 1

	return RecordingScript{
		Progress: []escriba.RecordingProgress{
			{Stage: escriba.RecordingStageQueued, QueuePosition: 1},
			{Stage: escriba.RecordingStageTranscribing, Percentage: 100},
			{Stage: escriba.RecordingStageLabelingSpeakers, Percentage: 100},
		},
		Recording: escriba.Recording{
			Text:     "Praxis Dr. Weber. Guten Tag. Ich brauche einen Termin.",
			Duration: 6 * time.Second,
			Language: escriba.DetectedLanguage{Language: "de", Probability: 0.98, Detected: true},
			Model:    "small",
			Segments: []escriba.Segment{
				{Index: 0, Start: 0, End: 2 * time.Second, Text: "Praxis Dr. Weber.", Speaker: &first},
				{Index: 1, Start: 2 * time.Second, End: 3 * time.Second, Text: "Guten Tag.", Speaker: &first},
				{Index: 2, Start: 4 * time.Second, End: 6 * time.Second, Text: "Ich brauche einen Termin.", Speaker: &second},
			},
			Speakers: []escriba.Speaker{
				{Index: 0, SpeakingTime: 3 * time.Second},
				{Index: 1, SpeakingTime: 2 * time.Second},
			},
		},
	}
}

func (h *escribaHandler) GetCapabilities(
	_ context.Context,
	_ *connect.Request[emptypb.Empty],
) (*connect.Response[escribapb.Capabilities], error) {
	return connect.NewResponse(capabilitiesResponse(h.fake.Capabilities)), nil
}

func (h *escribaHandler) script(cfg *escribapb.TranscribeLiveConfig, audio [][]byte) (LiveScript, error) {
	if h.fake.LiveFunc == nil {
		return cannedScript(), nil
	}

	return h.fake.LiveFunc(liveConfigFromProto(cfg), audio)
}

// ---------------------------------------------------------------------------
// facade -> proto
// ---------------------------------------------------------------------------

func liveConfigFromProto(cfg *escribapb.TranscribeLiveConfig) escriba.LiveConfig {
	out := escriba.LiveConfig{
		SampleRate:       int(cfg.GetSampleRate()),
		Language:         cfg.GetLanguage(),
		Model:            cfg.GetModel().GetModelId(),
		EnableRefinement: cfg.EnableRefinement,
	}

	if cfg.GetEncoding() == escribapb.AudioEncoding_AUDIO_ENCODING_PCM_S16LE {
		out.Encoding = escriba.EncodingPCMS16LE
	}

	return out
}

func readyResponse(cfg *escribapb.TranscribeLiveConfig) *escribapb.TranscribeLiveResponse {
	model := cfg.GetModel().GetModelId()
	if model == "" {
		model = "small"
	}

	return &escribapb.TranscribeLiveResponse{
		Event: &escribapb.TranscribeLiveResponse_Ready{
			Ready: &escribapb.SessionReady{
				Model:      &escribapb.TranscriptionModelRef{ModelId: model},
				Language:   cfg.GetLanguage(),
				SampleRate: cfg.GetSampleRate(),
			},
		},
	}
}

//nolint:exhaustive // only the events a script can express are mapped here.
func liveResponseFromEvent(event escriba.Event) (*escribapb.TranscribeLiveResponse, bool) {
	switch event.Kind {
	case escriba.EventKindPartial:
		return &escribapb.TranscribeLiveResponse{
			Event: &escribapb.TranscribeLiveResponse_Partial{
				Partial: &escribapb.PartialText{
					UtteranceIndex: int32(event.Partial.Utterance), //nolint:gosec // test data
					Text:           event.Partial.Text,
				},
			},
		}, true

	case escriba.EventKindCommitted:
		return &escribapb.TranscribeLiveResponse{
			Event: &escribapb.TranscribeLiveResponse_Committed{
				Committed: &escribapb.CommittedText{
					UtteranceIndex:            int32(event.Committed.Utterance), //nolint:gosec // test data
					Text:                      event.Committed.Text,
					SessionOffsetStartSeconds: event.Committed.Start.Seconds(),
					SessionOffsetEndSeconds:   event.Committed.End.Seconds(),
				},
			},
		}, true

	case escriba.EventKindUtteranceEnd:
		return &escribapb.TranscribeLiveResponse{
			Event: &escribapb.TranscribeLiveResponse_UtteranceEnd{
				UtteranceEnd: &escribapb.UtteranceEnd{
					UtteranceIndex: int32(event.UtteranceEnd.Utterance), //nolint:gosec // test data
				},
			},
		}, true

	case escriba.EventKindRevision:
		return &escribapb.TranscribeLiveResponse{
			Event: &escribapb.TranscribeLiveResponse_Revision{
				Revision: &escribapb.UtteranceRevision{
					UtteranceIndex: int32(event.Revision.Utterance), //nolint:gosec // test data
					Text:           event.Revision.Text,
				},
			},
		}, true

	default:
		return nil, false
	}
}

func completeResponse(complete escriba.Complete) *escribapb.TranscribeLiveResponse {
	utterances := make([]*escribapb.UtteranceText, 0, len(complete.Utterances))
	for _, utterance := range complete.Utterances {
		utterances = append(utterances, &escribapb.UtteranceText{
			UtteranceIndex: int32(utterance.Index), //nolint:gosec // test data
			Text:           utterance.Text,
			Refined:        utterance.Refined,
		})
	}

	reason := escribapb.TranscriptComplete_END_REASON_CLIENT_CLOSED
	if complete.Reason == escriba.EndReasonSessionLimit {
		reason = escribapb.TranscriptComplete_END_REASON_SESSION_LIMIT
	}

	return &escribapb.TranscribeLiveResponse{
		Event: &escribapb.TranscribeLiveResponse_Complete{
			Complete: &escribapb.TranscriptComplete{
				Text:         complete.Text,
				Language:     complete.Language,
				AudioSeconds: complete.Audio.Seconds(),
				Utterances:   utterances,
				Reason:       reason,
			},
		},
	}
}

func transcribeResponse(transcript escriba.Transcript) *escribapb.TranscribeResponse {
	words := make([]*escribapb.WordTiming, 0, len(transcript.Words))
	for _, word := range transcript.Words {
		words = append(words, &escribapb.WordTiming{
			StartSeconds: word.Start.Seconds(),
			EndSeconds:   word.End.Seconds(),
			Text:         word.Text,
		})
	}

	return &escribapb.TranscribeResponse{
		Text:            transcript.Text,
		DurationSeconds: transcript.Duration.Seconds(),
		Language: &escribapb.DetectedLanguage{
			Language:    transcript.Language.Language,
			Probability: transcript.Language.Probability,
			Detected:    transcript.Language.Detected,
		},
		Words: words,
		Model: &escribapb.TranscriptionModelRef{ModelId: transcript.Model},
	}
}

func recordingOptionsFromProto(cfg *escribapb.TranscribeRecordingConfig) escriba.RecordingOptions {
	opts := escriba.RecordingOptions{
		Language:     cfg.GetLanguage(),
		Model:        cfg.GetModel().GetModelId(),
		IncludeWords: cfg.GetIncludeWords(),
	}

	speakers := cfg.GetSpeakers()
	if speakers == nil {
		return opts
	}

	opts.Speakers = &escriba.SpeakerOptions{
		Method: escriba.SpeakerMethodDiarization,
		Count:  int(speakers.GetSpeakerCount()),
		Min:    int(speakers.GetMinSpeakers()),
		Max:    int(speakers.GetMaxSpeakers()),
	}
	if speakers.GetMethod() == escribapb.SpeakerLabeling_METHOD_CHANNEL {
		opts.Speakers.Method = escriba.SpeakerMethodChannel
	}

	return opts
}

func detectedLanguageProto(language escriba.DetectedLanguage) *escribapb.DetectedLanguage {
	return &escribapb.DetectedLanguage{
		Language:    language.Language,
		Probability: language.Probability,
		Detected:    language.Detected,
	}
}

func recordingResponses(script RecordingScript) []*escribapb.TranscribeRecordingResponse {
	recording := script.Recording
	model := &escribapb.TranscriptionModelRef{ModelId: recording.Model}

	responses := []*escribapb.TranscribeRecordingResponse{{
		Event: &escribapb.TranscribeRecordingResponse_Accepted{
			Accepted: &escribapb.RecordingAccepted{
				DurationSeconds: recording.Duration.Seconds(),
				Language:        detectedLanguageProto(recording.Language),
				Model:           model,
			},
		},
	}}

	stages := map[escriba.RecordingStage]escribapb.RecordingProgress_Stage{
		escriba.RecordingStageQueued:           escribapb.RecordingProgress_STAGE_QUEUED,
		escriba.RecordingStageTranscribing:     escribapb.RecordingProgress_STAGE_TRANSCRIBING,
		escriba.RecordingStageLabelingSpeakers: escribapb.RecordingProgress_STAGE_LABELING_SPEAKERS,
	}

	for _, progress := range script.Progress {
		responses = append(responses, &escribapb.TranscribeRecordingResponse{
			Event: &escribapb.TranscribeRecordingResponse_Progress{
				Progress: &escribapb.RecordingProgress{
					Stage:         stages[progress.Stage],
					Percentage:    progress.Percentage,
					QueuePosition: int32(progress.QueuePosition), //nolint:gosec // test data
				},
			},
		})
	}

	for _, segment := range recording.Segments {
		responses = append(responses, &escribapb.TranscribeRecordingResponse{
			Event: &escribapb.TranscribeRecordingResponse_Segment{Segment: segmentProto(segment)},
		})
	}

	speakers := make([]*escribapb.SpeakerSummary, 0, len(recording.Speakers))
	for _, speaker := range recording.Speakers {
		speakers = append(speakers, &escribapb.SpeakerSummary{
			SpeakerIndex:    int32(speaker.Index), //nolint:gosec // test data
			SpeakingSeconds: speaker.SpeakingTime.Seconds(),
		})
	}

	return append(responses, &escribapb.TranscribeRecordingResponse{
		Event: &escribapb.TranscribeRecordingResponse_Complete{
			Complete: &escribapb.RecordingComplete{
				Text:            recording.Text,
				DurationSeconds: recording.Duration.Seconds(),
				Language:        detectedLanguageProto(recording.Language),
				Model:           model,
				Speakers:        speakers,
				SegmentCount:    int32(len(recording.Segments)), //nolint:gosec // test data
			},
		},
	})
}

func segmentProto(segment escriba.Segment) *escribapb.TranscriptSegment {
	out := &escribapb.TranscriptSegment{
		Index:        int32(segment.Index), //nolint:gosec // test data
		StartSeconds: segment.Start.Seconds(),
		EndSeconds:   segment.End.Seconds(),
		Text:         segment.Text,
	}

	if segment.Speaker != nil {
		speaker := int32(*segment.Speaker) //nolint:gosec // test data
		out.SpeakerIndex = &speaker
	}

	for _, word := range segment.Words {
		out.Words = append(out.Words, &escribapb.WordTiming{
			StartSeconds: word.Start.Seconds(),
			EndSeconds:   word.End.Seconds(),
			Text:         word.Text,
		})
	}

	return out
}

func capabilitiesResponse(caps escriba.Capabilities) *escribapb.Capabilities {
	models := make([]*escribapb.TranscriptionModelInfo, 0, len(caps.Models))
	for _, model := range caps.Models {
		models = append(models, &escribapb.TranscriptionModelInfo{
			Model:        &escribapb.TranscriptionModelRef{ModelId: model.Model},
			Multilingual: model.Multilingual,
			Loaded:       model.Loaded,
			Description:  model.Description,
		})
	}

	return &escribapb.Capabilities{
		Models:              models,
		DefaultLanguage:     caps.DefaultLanguage,
		MaxSessionSeconds:   int32(caps.MaxSession.Seconds()),
		MaxRecordingSeconds: int32(caps.MaxRecording.Seconds()),
		RefinementEnabled:   caps.RefinementEnabled,

		MaxLongRecordingSeconds: int32(caps.MaxLongRecording.Seconds()),
		MaxRecordingBytes:       caps.MaxRecordingBytes,
		SpeakerMethods:          speakerMethodsProto(caps.SpeakerMethods),
	}
}

func speakerMethodsProto(methods []escriba.SpeakerMethod) []escribapb.SpeakerLabeling_Method {
	out := make([]escribapb.SpeakerLabeling_Method, 0, len(methods))

	for _, method := range methods {
		switch method {
		case escriba.SpeakerMethodDiarization:
			out = append(out, escribapb.SpeakerLabeling_METHOD_DIARIZATION)
		case escriba.SpeakerMethodChannel:
			out = append(out, escribapb.SpeakerLabeling_METHOD_CHANNEL)
		}
	}

	return out
}
