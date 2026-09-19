package escriba

import (
	"strings"
	"time"

	escribapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba"
)

// Encoding is the wire format of the audio a live session streams.
type Encoding string

const (
	// EncodingUnspecified lets the server pick, which today means PCM_S16LE.
	EncodingUnspecified Encoding = ""
	// EncodingPCMS16LE is signed 16-bit little-endian mono PCM.
	EncodingPCMS16LE Encoding = "pcm-s16le"
)

func (e Encoding) toProto() escribapb.AudioEncoding {
	if e == EncodingPCMS16LE {
		return escribapb.AudioEncoding_AUDIO_ENCODING_PCM_S16LE
	}

	return escribapb.AudioEncoding_AUDIO_ENCODING_UNSPECIFIED
}

// EndReason says why a live session ended.
type EndReason string

const (
	EndReasonUnspecified EndReason = ""
	// EndReasonClientClosed is the normal case: the client stopped sending.
	EndReasonClientClosed EndReason = "client-closed"
	// EndReasonSessionLimit means the deployment's per-session time limit was
	// reached. The transcript is complete and valid; open a new session.
	EndReasonSessionLimit EndReason = "session-limit"
)

// endReasonFromProto decodes forward-compatibly: an enum from a newer server is
// preserved as a readable string rather than dropped.
func endReasonFromProto(v escribapb.TranscriptComplete_EndReason) EndReason {
	switch v {
	case escribapb.TranscriptComplete_END_REASON_UNSPECIFIED:
		return EndReasonUnspecified
	case escribapb.TranscriptComplete_END_REASON_CLIENT_CLOSED:
		return EndReasonClientClosed
	case escribapb.TranscriptComplete_END_REASON_SESSION_LIMIT:
		return EndReasonSessionLimit
	default:
		name := strings.TrimPrefix(v.String(), "END_REASON_")

		return EndReason(strings.ReplaceAll(strings.ToLower(name), "_", "-"))
	}
}

// LiveConfig opens a live session.
type LiveConfig struct {
	// SampleRate of the PCM frames, in Hz. Required.
	//
	// 16000 is the native rate and avoids a resampling step. Browser clients
	// should open their AudioContext at 16 kHz and let the platform resample.
	SampleRate int

	// Language is a code such as "de". Optional; empty uses the deployment
	// default.
	//
	// Not a hint: the language is a token in the decoder's prompt, so it
	// constrains decoding. A live session never auto-detects — use Transcribe
	// when the language is genuinely unknown.
	Language string

	// Encoding of the audio frames. Empty means PCM_S16LE.
	Encoding Encoding

	// Model optionally pins a model. Empty uses the deployment default;
	// pinning one this deployment does not serve fails rather than falling back.
	Model string

	// EnableRefinement re-decodes each finished utterance at higher quality,
	// published as a Revision. Nil follows the deployment default.
	EnableRefinement *bool
}

func (c LiveConfig) toProto() *escribapb.TranscribeLiveConfig {
	cfg := &escribapb.TranscribeLiveConfig{
		SampleRate:       int32(c.SampleRate), //nolint:gosec // sample rates are far below int32 range
		Language:         c.Language,
		Encoding:         c.Encoding.toProto(),
		EnableRefinement: c.EnableRefinement,
	}

	if c.Model != "" {
		cfg.Model = &escribapb.TranscriptionModelRef{ModelId: c.Model}
	}

	return cfg
}

// EventKind identifies which field of an Event is populated.
type EventKind string

const (
	EventKindReady        EventKind = "ready"
	EventKindPartial      EventKind = "partial"
	EventKindCommitted    EventKind = "committed"
	EventKindUtteranceEnd EventKind = "utterance-end"
	EventKindRevision     EventKind = "revision"
	EventKindComplete     EventKind = "complete"
)

// Event is one live transcription event. Kind says which field to read.
type Event struct {
	Kind EventKind

	Ready     *Ready
	Partial   *Partial
	Committed *Committed
	// UtteranceEnd carries the index of the utterance that just closed.
	UtteranceEnd *UtteranceEnd
	Revision     *Revision
	Complete     *Complete
}

// Ready acknowledges the session and reports what is actually serving it.
type Ready struct {
	Model      string
	Language   string
	SampleRate int
}

// Partial is the current best guess for the tail of the open utterance.
//
// Provisional: replace the previously shown partial wholesale, never append. An
// empty Text means the tail was promoted and nothing provisional remains.
type Partial struct {
	Utterance int
	Text      string
}

// Committed is settled text, never retracted. Append it to the transcript, but
// keep it grouped by Utterance so a Revision can replace the whole group.
type Committed struct {
	Utterance int
	Text      string

	// Start and End locate this fragment within the SESSION, measured from the
	// first audio frame sent. Not comparable with the recording-relative
	// offsets in a Transcript's Words.
	Start time.Duration
	End   time.Duration
}

// UtteranceEnd reports that trailing silence closed an utterance.
type UtteranceEnd struct {
	Utterance int
}

// Revision replaces every committed word of one utterance with a
// higher-quality re-decode.
//
// It arrives out of band: newer utterances may stream while an older one is
// still being refined, so key on Utterance rather than arrival order. Delivery
// is best-effort — a client that ignores revisions still shows correct, if
// slightly worse, text.
type Revision struct {
	Utterance int
	Text      string
}

// Utterance is one utterance of a finished transcript.
type Utterance struct {
	Index int
	Text  string
	// Refined is true when this text came from the refinement re-decode rather
	// than the live stream.
	Refined bool
}

// Complete is the terminal event: the authoritative result of a live session.
//
// Its Text is refined wherever a refinement landed and is therefore not
// necessarily the concatenation of the Committed events observed.
type Complete struct {
	Text       string
	Language   string
	Audio      time.Duration
	Utterances []Utterance
	Reason     EndReason
}

func eventFromProto(resp *escribapb.TranscribeLiveResponse) (Event, bool) {
	switch {
	case resp.GetReady() != nil:
		ready := resp.GetReady()

		return Event{Kind: EventKindReady, Ready: &Ready{
			Model:      ready.GetModel().GetModelId(),
			Language:   ready.GetLanguage(),
			SampleRate: int(ready.GetSampleRate()),
		}}, true

	case resp.GetPartial() != nil:
		partial := resp.GetPartial()

		return Event{Kind: EventKindPartial, Partial: &Partial{
			Utterance: int(partial.GetUtteranceIndex()),
			Text:      partial.GetText(),
		}}, true

	case resp.GetCommitted() != nil:
		committed := resp.GetCommitted()

		return Event{Kind: EventKindCommitted, Committed: &Committed{
			Utterance: int(committed.GetUtteranceIndex()),
			Text:      committed.GetText(),
			Start:     seconds(committed.GetSessionOffsetStartSeconds()),
			End:       seconds(committed.GetSessionOffsetEndSeconds()),
		}}, true

	case resp.GetUtteranceEnd() != nil:
		return Event{Kind: EventKindUtteranceEnd, UtteranceEnd: &UtteranceEnd{
			Utterance: int(resp.GetUtteranceEnd().GetUtteranceIndex()),
		}}, true

	case resp.GetRevision() != nil:
		revision := resp.GetRevision()

		return Event{Kind: EventKindRevision, Revision: &Revision{
			Utterance: int(revision.GetUtteranceIndex()),
			Text:      revision.GetText(),
		}}, true

	case resp.GetComplete() != nil:
		return Event{Kind: EventKindComplete, Complete: completeFromProto(resp.GetComplete())}, true

	default:
		// A newer server may send an event this SDK predates; skipping it is
		// safer than failing the session.
		return Event{}, false
	}
}

func completeFromProto(msg *escribapb.TranscriptComplete) *Complete {
	utterances := make([]Utterance, 0, len(msg.GetUtterances()))
	for _, utterance := range msg.GetUtterances() {
		utterances = append(utterances, Utterance{
			Index:   int(utterance.GetUtteranceIndex()),
			Text:    utterance.GetText(),
			Refined: utterance.GetRefined(),
		})
	}

	return &Complete{
		Text:       msg.GetText(),
		Language:   msg.GetLanguage(),
		Audio:      seconds(msg.GetAudioSeconds()),
		Utterances: utterances,
		Reason:     endReasonFromProto(msg.GetReason()),
	}
}

// ---------------------------------------------------------------------------
// batch
// ---------------------------------------------------------------------------

// TranscribeOptions tunes a one-shot transcription. All fields are optional.
type TranscribeOptions struct {
	// Language pins the decoding language. Empty detects it and falls back to
	// the deployment default when no probe is confident.
	Language string

	// Model pins a model; empty uses the deployment default.
	Model string

	// IncludeWords returns per-word timings. Off by default: the alignment pass
	// costs time and most callers only want the text.
	IncludeWords bool
}

// DetectedLanguage reports which language was used and whether it was actually
// determined from the audio or merely assumed.
type DetectedLanguage struct {
	Language string
	// Probability of the winning language. When Detected is false this is still
	// the best score any probe achieved.
	Probability float32
	// Detected is false when the deployment default was used instead, or when
	// the caller pinned a language and no detection ran.
	Detected bool
}

// Word is one word with its position in the recording.
type Word struct {
	Start time.Duration
	End   time.Duration
	Text  string
}

// Transcript is the result of a one-shot transcription.
type Transcript struct {
	Text     string
	Duration time.Duration
	Language DetectedLanguage
	// Words is populated only when TranscribeOptions.IncludeWords was set.
	Words []Word
	// Model that produced this transcript. Persist it alongside the text to
	// enable targeted recomputation when the model changes.
	Model string
}

func transcriptFromProto(msg *escribapb.TranscribeResponse) *Transcript {
	words := make([]Word, 0, len(msg.GetWords()))
	for _, word := range msg.GetWords() {
		words = append(words, Word{
			Start: seconds(word.GetStartSeconds()),
			End:   seconds(word.GetEndSeconds()),
			Text:  word.GetText(),
		})
	}

	return &Transcript{
		Text:     msg.GetText(),
		Duration: seconds(msg.GetDurationSeconds()),
		Language: DetectedLanguage{
			Language:    msg.GetLanguage().GetLanguage(),
			Probability: msg.GetLanguage().GetProbability(),
			Detected:    msg.GetLanguage().GetDetected(),
		},
		Words: words,
		Model: msg.GetModel().GetModelId(),
	}
}

// ---------------------------------------------------------------------------
// recordings
// ---------------------------------------------------------------------------

// SpeakerMethod is how the speakers of a recording are told apart.
type SpeakerMethod string

const (
	// SpeakerMethodDiarization clusters the voices in the recording. Works on
	// any audio, costs a second model pass, and is only offered by deployments
	// built for it — see Capabilities.SpeakerMethods.
	SpeakerMethodDiarization SpeakerMethod = "diarization"

	// SpeakerMethodChannel treats each audio channel as one speaker: exact and
	// free, for dual-channel call recordings. Needs a stereo file.
	SpeakerMethodChannel SpeakerMethod = "channel"
)

func (m SpeakerMethod) toProto() (escribapb.SpeakerLabeling_Method, error) {
	switch m {
	case "", SpeakerMethodDiarization:
		return escribapb.SpeakerLabeling_METHOD_DIARIZATION, nil
	case SpeakerMethodChannel:
		return escribapb.SpeakerLabeling_METHOD_CHANNEL, nil
	default:
		return 0, invalidArgument("escriba: unknown speaker method " + string(m))
	}
}

func speakerMethodFromProto(m escribapb.SpeakerLabeling_Method) (SpeakerMethod, bool) {
	switch m {
	case escribapb.SpeakerLabeling_METHOD_DIARIZATION:
		return SpeakerMethodDiarization, true
	case escribapb.SpeakerLabeling_METHOD_CHANNEL:
		return SpeakerMethodChannel, true
	case escribapb.SpeakerLabeling_METHOD_UNSPECIFIED:
	}

	// Unspecified, or a method added after this SDK version was built.
	return "", false
}

// SpeakerOptions asks for a recording to be attributed to speakers. Speakers
// are told apart, not identified: each gets an index, numbered by first
// appearance.
type SpeakerOptions struct {
	// Method defaults to SpeakerMethodDiarization.
	Method SpeakerMethod

	// Count fixes the number of speakers when it is known, which is noticeably
	// more accurate than letting the server estimate it. Diarization only, and
	// exclusive with Min and Max.
	Count int

	// Min and Max bound the estimate when the exact count is not known. Zero
	// leaves that side open. Diarization only.
	Min int
	Max int
}

func (o *SpeakerOptions) toProto() (*escribapb.SpeakerLabeling, error) {
	if o == nil {
		return nil, nil //nolint:nilnil // absent options are an absent message, not an error
	}

	method, err := o.Method.toProto()
	if err != nil {
		return nil, err
	}

	switch {
	case o.Count < 0 || o.Min < 0 || o.Max < 0:
		return nil, invalidArgument("escriba: speaker counts must not be negative")
	case o.Count != 0 && (o.Min != 0 || o.Max != 0):
		return nil, invalidArgument("escriba: SpeakerOptions.Count excludes Min and Max")
	case o.Max != 0 && o.Min > o.Max:
		return nil, invalidArgument("escriba: SpeakerOptions.Min must not exceed Max")
	case o.Method == SpeakerMethodChannel && (o.Count != 0 || o.Min != 0 || o.Max != 0):
		return nil, invalidArgument("escriba: speaker counts do not apply to SpeakerMethodChannel")
	}

	return &escribapb.SpeakerLabeling{
		Method:       method,
		SpeakerCount: int32(o.Count), //nolint:gosec // validated non-negative; a speaker count cannot overflow
		MinSpeakers:  int32(o.Min),   //nolint:gosec // as above
		MaxSpeakers:  int32(o.Max),   //nolint:gosec // as above
	}, nil
}

// RecordingOptions tunes TranscribeRecording. All fields are optional.
type RecordingOptions struct {
	// Language pins the decoding language. Empty detects it and falls back to
	// the deployment default when no probe is confident.
	Language string

	// Model pins a model; empty uses the deployment default.
	Model string

	// IncludeWords returns per-word timings on every segment.
	IncludeWords bool

	// Speakers requests speaker labels. Nil for a plain transcript.
	Speakers *SpeakerOptions

	// OnProgress, when set, is told where the server is. Called from the
	// goroutine that called TranscribeRecording, so it must not block for long.
	OnProgress func(RecordingProgress)

	// OnSegment, when set, receives each finished segment as it arrives. The
	// returned Recording carries all of them regardless.
	OnSegment func(Segment)
}

func (o *RecordingOptions) toProto(s *Service) (*escribapb.TranscribeRecordingConfig, error) {
	speakers, err := o.Speakers.toProto()
	if err != nil {
		return nil, err
	}

	cfg := &escribapb.TranscribeRecordingConfig{
		Language:     s.languageOr(o.Language),
		IncludeWords: o.IncludeWords,
		Speakers:     speakers,
	}
	if model := s.modelOr(o.Model); model != "" {
		cfg.Model = &escribapb.TranscriptionModelRef{ModelId: model}
	}

	return cfg, nil
}

// RecordingStage is the phase of work a RecordingProgress refers to.
type RecordingStage string

const (
	// RecordingStageQueued: waiting for the server, which processes one
	// recording at a time and gives live sessions priority over all of them.
	RecordingStageQueued RecordingStage = "queued"

	RecordingStageTranscribing RecordingStage = "transcribing"

	// RecordingStageLabelingSpeakers only occurs with SpeakerMethodDiarization.
	RecordingStageLabelingSpeakers RecordingStage = "labeling_speakers"
)

// RecordingProgress reports where the server is with a recording.
type RecordingProgress struct {
	Stage RecordingStage
	// Percentage of the current stage, 0–100. Restarts at 0 on a stage change.
	Percentage float32
	// QueuePosition is the number of recordings ahead of this one. Meaningful
	// only while Stage is RecordingStageQueued.
	QueuePosition int
}

func recordingProgressFromProto(msg *escribapb.RecordingProgress) RecordingProgress {
	stages := map[escribapb.RecordingProgress_Stage]RecordingStage{
		escribapb.RecordingProgress_STAGE_QUEUED:            RecordingStageQueued,
		escribapb.RecordingProgress_STAGE_TRANSCRIBING:      RecordingStageTranscribing,
		escribapb.RecordingProgress_STAGE_LABELING_SPEAKERS: RecordingStageLabelingSpeakers,
	}

	return RecordingProgress{
		Stage:         stages[msg.GetStage()],
		Percentage:    msg.GetPercentage(),
		QueuePosition: int(msg.GetQueuePosition()),
	}
}

// Segment is one finished piece of a recording's transcript: a sentence or
// phrase, in the unit a subtitle would use. A segment never spans a change of
// speaker.
type Segment struct {
	Index int
	Start time.Duration
	End   time.Duration
	Text  string
	// Speaker is who said it: a 0-based index, numbered by first appearance.
	// Nil when no speaker labels were requested.
	Speaker *int
	// Words is populated only when RecordingOptions.IncludeWords was set.
	Words []Word
}

func segmentFromProto(msg *escribapb.TranscriptSegment) Segment {
	segment := Segment{
		Index: int(msg.GetIndex()),
		Start: seconds(msg.GetStartSeconds()),
		End:   seconds(msg.GetEndSeconds()),
		Text:  msg.GetText(),
	}

	if msg.SpeakerIndex != nil {
		speaker := int(msg.GetSpeakerIndex())
		segment.Speaker = &speaker
	}

	for _, word := range msg.GetWords() {
		segment.Words = append(segment.Words, Word{
			Start: seconds(word.GetStartSeconds()),
			End:   seconds(word.GetEndSeconds()),
			Text:  word.GetText(),
		})
	}

	return segment
}

// Speaker describes one speaker found in a recording.
type Speaker struct {
	Index int
	// SpeakingTime is the total duration of this speaker's segments.
	SpeakingTime time.Duration
}

// Recording is the result of TranscribeRecording.
type Recording struct {
	// Text is the full transcript, without speaker labels.
	Text     string
	Duration time.Duration
	Language DetectedLanguage
	// Model that produced this transcript. Persist it alongside the text to
	// enable targeted recomputation when the model changes.
	Model    string
	Segments []Segment
	// Speakers found, ordered by index. Empty when none were requested.
	Speakers []Speaker

	segmentCount int
}

// Turn is a run of consecutive segments by one speaker, merged for reading.
type Turn struct {
	// Speaker is nil when the recording has no speaker labels, in which case
	// the whole transcript is a single turn.
	Speaker *int
	Start   time.Duration
	End     time.Duration
	Text    string
}

// Turns merges consecutive segments of the same speaker: the shape a dialogue
// is read in, where Segments is the shape it is subtitled in.
func (r *Recording) Turns() []Turn {
	var turns []Turn

	for _, segment := range r.Segments {
		if last := len(turns) - 1; last >= 0 && sameSpeaker(turns[last].Speaker, segment.Speaker) {
			turns[last].Text += " " + segment.Text
			turns[last].End = segment.End

			continue
		}

		turns = append(turns, Turn{
			Speaker: segment.Speaker,
			Start:   segment.Start,
			End:     segment.End,
			Text:    segment.Text,
		})
	}

	return turns
}

func (r *Recording) applyComplete(msg *escribapb.RecordingComplete) {
	r.Text = msg.GetText()
	r.Duration = seconds(msg.GetDurationSeconds())
	r.Language = DetectedLanguage{
		Language:    msg.GetLanguage().GetLanguage(),
		Probability: msg.GetLanguage().GetProbability(),
		Detected:    msg.GetLanguage().GetDetected(),
	}
	r.Model = msg.GetModel().GetModelId()
	r.segmentCount = int(msg.GetSegmentCount())

	for _, speaker := range msg.GetSpeakers() {
		r.Speakers = append(r.Speakers, Speaker{
			Index:        int(speaker.GetSpeakerIndex()),
			SpeakingTime: seconds(speaker.GetSpeakingSeconds()),
		})
	}
}

func sameSpeaker(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// ---------------------------------------------------------------------------
// discovery
// ---------------------------------------------------------------------------

// ModelInfo describes one model a deployment can serve.
type ModelInfo struct {
	Model string
	// Multilingual is false for English-only builds, which reject any other
	// language.
	Multilingual bool
	// Loaded is true when the weights are resident and the model can serve now.
	Loaded      bool
	Description string
}

// Capabilities describes what a deployment serves and the limits to design
// around. Fetch once at startup; refresh on reconnect.
type Capabilities struct {
	Models          []ModelInfo
	DefaultLanguage string
	// MaxSession is how long a live session may run before the server ends it
	// with EndReasonSessionLimit. Zero means unlimited: plan to roll over to a
	// new session before it elapses.
	MaxSession time.Duration
	// MaxRecording is the longest recording Transcribe accepts. Zero means
	// unlimited.
	MaxRecording time.Duration
	// RefinementEnabled is the default a live session inherits when
	// LiveConfig.EnableRefinement is nil.
	RefinementEnabled bool
	// MaxLongRecording is the longest recording TranscribeRecording accepts.
	// Zero means unlimited.
	MaxLongRecording time.Duration
	// MaxRecordingBytes is the largest upload TranscribeRecording accepts. Zero
	// means unlimited.
	MaxRecordingBytes int64
	// SpeakerMethods are the speaker labelling methods this deployment offers.
	// Asking for one that is not listed fails with CodeUnimplemented.
	SpeakerMethods []SpeakerMethod
}

func capabilitiesFromProto(msg *escribapb.Capabilities) *Capabilities {
	models := make([]ModelInfo, 0, len(msg.GetModels()))
	for _, model := range msg.GetModels() {
		models = append(models, ModelInfo{
			Model:        model.GetModel().GetModelId(),
			Multilingual: model.GetMultilingual(),
			Loaded:       model.GetLoaded(),
			Description:  model.GetDescription(),
		})
	}

	var methods []SpeakerMethod

	for _, method := range msg.GetSpeakerMethods() {
		// A method this SDK version cannot request is of no use to its caller.
		if known, ok := speakerMethodFromProto(method); ok {
			methods = append(methods, known)
		}
	}

	return &Capabilities{
		Models:            models,
		DefaultLanguage:   msg.GetDefaultLanguage(),
		MaxSession:        time.Duration(msg.GetMaxSessionSeconds()) * time.Second,
		MaxRecording:      time.Duration(msg.GetMaxRecordingSeconds()) * time.Second,
		RefinementEnabled: msg.GetRefinementEnabled(),
		MaxLongRecording:  time.Duration(msg.GetMaxLongRecordingSeconds()) * time.Second,
		MaxRecordingBytes: msg.GetMaxRecordingBytes(),
		SpeakerMethods:    methods,
	}
}

func seconds(v float64) time.Duration {
	return time.Duration(v * float64(time.Second))
}
