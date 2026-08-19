package escriba

import (
	"context"
	"errors"
	"io"
	"iter"
	"sync"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/internal/apierr"
	escribapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba/escribaconnect"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// ErrLiveClosed is returned when a session is used after it has finished.
var ErrLiveClosed = errors.New("rootpuller escriba: live session closed")

// eventBuffer is how many events may queue ahead of a slow consumer before the
// receive loop blocks, which in turn applies backpressure to the server.
const eventBuffer = 32

// LiveSession is one live transcription stream.
//
// Send audio with Send, read results with Events, and signal that the speaker
// has finished with CloseSend. Events keeps yielding until the terminal
// Complete arrives, so the authoritative transcript is never lost by closing
// early. Always Close a session.
//
// Send may be called from a different goroutine than Events, but Events has a
// single consumer.
type LiveSession struct {
	stream    *connect.BidiStreamForClient[escribapb.TranscribeLiveRequest, escribapb.TranscribeLiveResponse]
	procedure string

	// connect streams are not safe for concurrent sends.
	sendMu    sync.Mutex
	sendClose sync.Once
	closeErr  error

	events   chan liveFrame
	recvDone chan struct{}

	mu     sync.Mutex
	err    error
	result *Complete
}

type liveFrame struct {
	event    Event
	terminal bool
	err      error
}

// OpenLive starts a live session. The config frame is sent before returning, so
// a rejected configuration surfaces here rather than on the first Send.
func (s *Service) OpenLive(ctx context.Context, cfg LiveConfig) (*LiveSession, error) {
	if cfg.SampleRate <= 0 {
		return nil, invalidArgument("escriba: LiveConfig.SampleRate is required")
	}

	if cfg.Language == "" {
		cfg.Language = s.defaultLanguage
	}

	if cfg.Model == "" {
		cfg.Model = s.defaultModel
	}

	// Pinned once, for the whole stream: a live session belongs entirely to the
	// deployment it opened on, so there is nothing to re-route mid-session.
	ctx = transport.EnsureEscribaDeployment(ctx, s.deployment)
	procedure := escribaconnect.TranscriptionServiceTranscribeLiveProcedure
	stream := s.rpc.TranscribeLive(ctx)

	if err := stream.Send(&escribapb.TranscribeLiveRequest{
		Frame: &escribapb.TranscribeLiveRequest_Config{Config: cfg.toProto()},
	}); err != nil {
		_ = stream.CloseRequest()
		_ = stream.CloseResponse()

		return nil, transport.WrapError(err, procedure)
	}

	session := &LiveSession{
		stream:    stream,
		procedure: procedure,
		events:    make(chan liveFrame, eventBuffer),
		recvDone:  make(chan struct{}),
	}

	go session.recvLoop()

	return session, nil
}

// Send forwards one frame of captured audio.
//
// Send roughly 100-250 ms per call: smaller frames add overhead without
// improving latency, because the server decodes on its own cadence.
func (l *LiveSession) Send(pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}

	l.sendMu.Lock()
	defer l.sendMu.Unlock()

	if err := l.stream.Send(&escribapb.TranscribeLiveRequest{
		Frame: &escribapb.TranscribeLiveRequest_Audio{
			Audio: &escribapb.TranscribeLiveRequest_AudioChunk{Data: pcm},
		},
	}); err != nil {
		// The definitive reason is on the receive side; surface it from Events.
		if errors.Is(err, io.EOF) {
			return ErrLiveClosed
		}

		return transport.WrapError(err, l.procedure)
	}

	return nil
}

// CloseSend signals that the speaker has finished.
//
// The session stays open: the server still has to finalise, so keep reading
// Events until Complete arrives.
func (l *LiveSession) CloseSend() error {
	l.sendMu.Lock()
	defer l.sendMu.Unlock()

	l.sendClose.Do(func() {
		l.closeErr = l.stream.CloseRequest()
	})

	if l.closeErr != nil {
		return transport.WrapError(l.closeErr, l.procedure)
	}

	return nil
}

// Events yields transcription events until the session ends.
//
// Read the two text events differently: Partial is provisional and must be
// overwritten wholesale, Committed is settled and safe to append. A Revision
// then replaces one utterance entirely, so keep committed text grouped by
// utterance index rather than as one flat string.
//
// The sequence ends after Complete, or with an error. Breaking out early stops
// consuming but does not close the session; call Close for that.
func (l *LiveSession) Events(ctx context.Context) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		for {
			select {
			case frame, ok := <-l.events:
				if !ok {
					return
				}

				if frame.err != nil {
					yield(Event{}, frame.err)

					return
				}

				if !yield(frame.event, nil) {
					return
				}

				if frame.terminal {
					return
				}

			case <-ctx.Done():
				yield(Event{}, apierr.New(connect.CodeCanceled, ctx.Err().Error(), l.procedure, 0, ctx.Err()))

				return
			}
		}
	}
}

// Result returns the terminal transcript once Events has finished.
func (l *LiveSession) Result() (*Complete, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.result, l.result != nil
}

// Close releases the session. Safe to call more than once.
func (l *LiveSession) Close() error {
	_ = l.CloseSend()

	<-l.recvDone

	if err := l.stream.CloseResponse(); err != nil && !errors.Is(err, io.EOF) {
		return transport.WrapError(err, l.procedure)
	}

	return nil
}

// Err reports why the session ended, if it failed.
func (l *LiveSession) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.err
}

// recvLoop is the single reader. It converts frames and closes the event
// channel exactly once, so Events always terminates.
func (l *LiveSession) recvLoop() {
	defer close(l.recvDone)
	defer close(l.events)

	for {
		resp, err := l.stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// A stream that ends without Complete lost the authoritative
				// transcript; say so rather than reporting a clean finish.
				if _, done := l.Result(); !done {
					l.fail(apierr.ErrMissingTerminal)
				}

				return
			}

			l.fail(transport.WrapError(err, l.procedure))

			return
		}

		event, ok := eventFromProto(resp)
		if !ok {
			continue
		}

		if event.Kind == EventKindComplete {
			l.mu.Lock()
			l.result = event.Complete
			l.mu.Unlock()
		}

		l.events <- liveFrame{event: event, terminal: event.Kind == EventKindComplete}

		if event.Kind == EventKindComplete {
			return
		}
	}
}

func (l *LiveSession) fail(err error) {
	l.mu.Lock()
	l.err = err
	l.mu.Unlock()

	l.events <- liveFrame{err: err}
}
