package webcontent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"iter"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/internal/apierr"
	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent/webcontentconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// SessionOptions tunes OpenSession. The fetcher (engine, proxy,
// fingerprint) is fixed for the whole session; jobs and screenshot apply
// to every instruction. Nil keeps all defaults.
type SessionOptions struct {
	Fetcher *FetcherOptions
	// IdleTimeout closes the session server-side after inactivity; 0
	// keeps the server default.
	IdleTimeout time.Duration
	Jobs        []ExtractionJob
	Screenshot  *ScreenshotOptions
}

// InstructionOptions tunes one session fetch. Nil keeps all defaults.
type InstructionOptions struct {
	Headers []Header
	Cookies map[string]string
	// WaitForSelector waits for the CSS selector before returning; empty
	// leaves it unset.
	WaitForSelector string
	// Wait is a fixed wait after load; 0 leaves it unset.
	Wait time.Duration
}

// ErrSessionClosed reports a Fetch on a session that was closed by
// Close or by the server.
var ErrSessionClosed = errors.New("rootpuller webcontent: session closed")

// Session is one ScrapeService/FetchSession stream: a persistent browser
// context (cookies, fingerprint, connection reuse) that fetches URLs on
// demand. Fetch and FetchEvents may be called concurrently; responses are
// demultiplexed by correlation ID. Always Close a session.
type Session struct {
	stream    *connect.BidiStreamForClient[webcontentpb.FetchSessionRequest, webcontentpb.FetchSessionResponse]
	procedure string

	sendMu sync.Mutex

	mu     sync.Mutex
	calls  map[string]*sessionCall
	err    error // session-fatal error; nil while healthy
	closed chan struct{}

	recvDone chan struct{}
}

type sessionFrame struct {
	event    Event
	terminal bool
	err      error // terminal error (*ContentError) or session failure
}

type sessionCall struct {
	frames chan sessionFrame
	gone   chan struct{} // closed when the caller stops listening
}

// OpenSession calls ScrapeService/FetchSession: sends the init frame and
// starts the response demultiplexer. A nil opts keeps all server
// defaults.
func (s *ScrapeService) OpenSession(ctx context.Context, opts *SessionOptions) (*Session, error) {
	// The bot identity is fixed at stream creation; it applies to every
	// instruction of the session.
	ctx = transport.EnsureBot(ctx, s.bot)

	if opts == nil {
		opts = &SessionOptions{}
	}

	fetcher, err := opts.Fetcher.toProto()
	if err != nil {
		return nil, err
	}

	jobs, err := jobsToProto(opts.Jobs)
	if err != nil {
		return nil, err
	}

	screenshot, err := opts.Screenshot.toProto()
	if err != nil {
		return nil, err
	}

	init := &webcontentpb.FetchSessionRequest_Init{
		Fetcher:    fetcher,
		Jobs:       jobs,
		Screenshot: screenshot,
	}
	if opts.IdleTimeout > 0 {
		init.IdleTimeoutSeconds = protoconv.DurationToSecondsPtr(&opts.IdleTimeout)
	}

	procedure := webcontentconnect.ScrapeServiceFetchSessionProcedure

	stream := s.rpc.FetchSession(ctx)
	if err := stream.Send(&webcontentpb.FetchSessionRequest{
		Message: &webcontentpb.FetchSessionRequest_Init_{Init: init},
	}); err != nil {
		_ = stream.CloseRequest()
		_ = stream.CloseResponse()

		return nil, transport.WrapError(err, procedure)
	}

	session := &Session{
		stream:    stream,
		procedure: procedure,
		calls:     map[string]*sessionCall{},
		closed:    make(chan struct{}),
		recvDone:  make(chan struct{}),
	}
	go session.recvLoop()

	return session, nil
}

func sessionFrameFromProto(pb *webcontentpb.FetchSessionResponse) (sessionFrame, bool) {
	switch m := pb.GetMessage().(type) {
	case *webcontentpb.FetchSessionResponse_FetchProgress:
		return sessionFrame{event: fetchProgressFromProto(m.FetchProgress)}, true
	case *webcontentpb.FetchSessionResponse_PageMetadata:
		return sessionFrame{event: PageMetadataEvent{Metadata: pageMetadataFromProto(m.PageMetadata)}}, true
	case *webcontentpb.FetchSessionResponse_Artifact:
		return sessionFrame{event: artifactChunkFromProto(m.Artifact)}, true
	case *webcontentpb.FetchSessionResponse_ExtractionProgress:
		return sessionFrame{event: extractionProgressFromProto(m.ExtractionProgress)}, true
	case *webcontentpb.FetchSessionResponse_ExtractionMetadata:
		return sessionFrame{event: ExtractionMetadataEvent{Metadata: extractionMetadataFromProto(m.ExtractionMetadata)}}, true
	case *webcontentpb.FetchSessionResponse_KindError:
		return sessionFrame{event: kindErrorFromProto(m.KindError)}, true
	case *webcontentpb.FetchSessionResponse_Error:
		return sessionFrame{terminal: true, err: contentErrorFromProto(m.Error)}, true
	case *webcontentpb.FetchSessionResponse_Done:
		return sessionFrame{event: DoneEvent{Summary: summaryFromProto(m.Done)}, terminal: true}, true
	default:
		return sessionFrame{}, false
	}
}

// FetchEvents sends one instruction and yields its event frames as they
// arrive, ending with a DoneEvent (or the page's *ContentError as the
// iteration error). Concurrent FetchEvents/Fetch calls on one session
// are pipelined server-side. A nil opts keeps all defaults.
func (s *Session) FetchEvents(ctx context.Context, url string, opts *InstructionOptions) iter.Seq2[Event, error] {
	s.mu.Lock()
	sessionErr := s.err
	s.mu.Unlock()

	if sessionErr != nil {
		return errSeq[Event](sessionErr)
	}

	if opts == nil {
		opts = &InstructionOptions{}
	}

	instruction := &webcontentpb.FetchSessionRequest_Instruction{
		Url:     url,
		Headers: headersToProto(opts.Headers),
		Cookies: opts.Cookies,
	}
	if opts.WaitForSelector != "" {
		instruction.WaitForSelector = &opts.WaitForSelector
	}

	if opts.Wait > 0 {
		instruction.WaitMilliseconds = protoconv.DurationToMsPtr(&opts.Wait)
	}

	id, call := s.register()
	instruction.CorrelationId = &id

	s.sendMu.Lock()
	err := s.stream.Send(&webcontentpb.FetchSessionRequest{
		Message: &webcontentpb.FetchSessionRequest_Instruction_{Instruction: instruction},
	})
	s.sendMu.Unlock()

	if err != nil {
		s.unregister(id, call)

		return errSeq[Event](transport.WrapError(err, s.procedure))
	}

	return func(yield func(Event, error) bool) {
		defer s.unregister(id, call)

		for {
			select {
			case frame := <-call.frames:
				if !yieldFrame(yield, frame) {
					return
				}
			case <-s.closed:
				s.yieldAfterClose(yield, call)

				return
			case <-ctx.Done():
				yield(nil, apierr.New(connect.CodeCanceled, ctx.Err().Error(), s.procedure, 0, ctx.Err()))

				return
			}
		}
	}
}

// Fetch sends one instruction and blocks until its terminal frame,
// returning the buffered result. A nil opts keeps all defaults.
func (s *Session) Fetch(ctx context.Context, url string, opts *InstructionOptions) (*FetchResult, error) {
	return CollectEvents(s.FetchEvents(ctx, url, opts))
}

// Close half-closes the session; the server drains in-flight
// instructions and ends the stream. Pending calls fail with
// ErrSessionClosed.
func (s *Session) Close() error {
	err := s.stream.CloseRequest()
	<-s.recvDone

	_ = s.stream.CloseResponse()
	if err != nil && !errors.Is(err, io.EOF) {
		return transport.WrapError(err, s.procedure)
	}

	return nil
}

// recvLoop is the single reader of the stream: it routes every response
// frame to the call registered under its correlation ID. Frames for
// unknown IDs are dropped.
func (s *Session) recvLoop() {
	defer close(s.recvDone)

	for {
		resp, err := s.stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.fail(ErrSessionClosed)
			} else {
				s.fail(transport.WrapError(err, s.procedure))
			}

			return
		}

		frame, ok := sessionFrameFromProto(resp)
		if !ok {
			continue
		}

		s.mu.Lock()
		call := s.calls[resp.GetCorrelationId()]
		s.mu.Unlock()

		if call == nil {
			continue
		}

		select {
		case call.frames <- frame:
		case <-call.gone:
		}
	}
}

func (s *Session) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return
	}

	s.err = err
	close(s.closed)
}

func (s *Session) register() (string, *sessionCall) {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	id := hex.EncodeToString(buf)
	call := &sessionCall{frames: make(chan sessionFrame, 16), gone: make(chan struct{})}

	s.mu.Lock()
	s.calls[id] = call
	s.mu.Unlock()

	return id, call
}

func (s *Session) unregister(id string, call *sessionCall) {
	s.mu.Lock()
	delete(s.calls, id)
	s.mu.Unlock()
	close(call.gone)
}

// yieldAfterClose drains the frames routed to call before the session
// failure, then yields the session-fatal error — unless a drained frame
// already ended the iteration (terminal frame, frame error, or consumer
// break).
func (s *Session) yieldAfterClose(yield func(Event, error) bool, call *sessionCall) {
	for {
		select {
		case frame := <-call.frames:
			if !yieldFrame(yield, frame) {
				return
			}
		default:
			s.mu.Lock()
			err := s.err
			s.mu.Unlock()
			yield(nil, err)

			return
		}
	}
}

// yieldFrame delivers one routed frame to the iterator, reporting whether
// iteration should continue: false on a frame error, a terminal frame, or
// when the consumer breaks out of the loop.
func yieldFrame(yield func(Event, error) bool, frame sessionFrame) bool {
	if frame.err != nil {
		yield(nil, frame.err)

		return false
	}

	if !yield(frame.event, nil) {
		return false
	}

	return !frame.terminal
}
