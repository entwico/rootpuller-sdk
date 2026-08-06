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

	"github.com/entwico/rootpuller-sdk/apierror"
	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent/webcontentconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// SessionInit configures OpenSession. The fetcher (engine, proxy,
// fingerprint) is fixed for the whole session; jobs and screenshot apply
// to every instruction.
type SessionInit struct {
	Fetcher *FetcherOptions
	// IdleTimeout closes the session server-side after inactivity.
	IdleTimeout *time.Duration
	Jobs        []ExtractionJob
	Screenshot  *ScreenshotOptions
}

// Instruction navigates the session to one URL.
type Instruction struct {
	URL             string
	Headers         []Header
	Cookies         map[string]string
	WaitForSelector *string
	Wait            *time.Duration
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
// starts the response demultiplexer. A nil init keeps all server
// defaults.
func (c *ScrapeClient) OpenSession(ctx context.Context, init *SessionInit) (*Session, error) {
	if init == nil {
		init = &SessionInit{}
	}
	fetcher, err := init.Fetcher.toProto()
	if err != nil {
		return nil, err
	}
	jobs, err := jobsToProto(init.Jobs)
	if err != nil {
		return nil, err
	}
	screenshot, err := init.Screenshot.toProto()
	if err != nil {
		return nil, err
	}
	procedure := webcontentconnect.ScrapeServiceFetchSessionProcedure
	stream := c.rpc.FetchSession(ctx)
	if err := stream.Send(&webcontentpb.FetchSessionRequest{
		Message: &webcontentpb.FetchSessionRequest_Init_{Init: &webcontentpb.FetchSessionRequest_Init{
			Fetcher:            fetcher,
			IdleTimeoutSeconds: protoconv.DurationToSecondsPtr(init.IdleTimeout),
			Jobs:               jobs,
			Screenshot:         screenshot,
		}},
	}); err != nil {
		_ = stream.CloseRequest()
		_ = stream.CloseResponse()
		return nil, transport.WrapError(err, procedure)
	}

	s := &Session{
		stream:    stream,
		procedure: procedure,
		calls:     map[string]*sessionCall{},
		closed:    make(chan struct{}),
		recvDone:  make(chan struct{}),
	}
	go s.recvLoop()
	return s, nil
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

// FetchEvents sends one instruction and yields its event frames as they
// arrive, ending with a DoneEvent (or the page's *ContentError as the
// iteration error). Concurrent FetchEvents/Fetch calls on one session
// are pipelined server-side.
func (s *Session) FetchEvents(ctx context.Context, instr *Instruction) iter.Seq2[Event, error] {
	s.mu.Lock()
	sessionErr := s.err
	s.mu.Unlock()
	if sessionErr != nil {
		return errSeq[Event](sessionErr)
	}

	id, call := s.register()
	s.sendMu.Lock()
	err := s.stream.Send(&webcontentpb.FetchSessionRequest{
		Message: &webcontentpb.FetchSessionRequest_Instruction_{Instruction: &webcontentpb.FetchSessionRequest_Instruction{
			CorrelationId:    &id,
			Url:              instr.URL,
			Headers:          headersToProto(instr.Headers),
			Cookies:          instr.Cookies,
			WaitForSelector:  instr.WaitForSelector,
			WaitMilliseconds: protoconv.DurationToMsPtr(instr.Wait),
		}},
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
				if frame.err != nil {
					yield(nil, frame.err)
					return
				}
				if !yield(frame.event, nil) {
					return
				}
				if frame.terminal {
					return
				}
			case <-s.closed:
				// Drain anything routed before the failure.
				for {
					select {
					case frame := <-call.frames:
						if frame.err != nil {
							yield(nil, frame.err)
							return
						}
						if !yield(frame.event, nil) {
							return
						}
						if frame.terminal {
							return
						}
						continue
					default:
					}
					break
				}
				s.mu.Lock()
				err := s.err
				s.mu.Unlock()
				yield(nil, err)
				return
			case <-ctx.Done():
				yield(nil, apierror.New(connect.CodeCanceled, ctx.Err().Error(), s.procedure, 0, ctx.Err()))
				return
			}
		}
	}
}

// Fetch sends one instruction and blocks until its terminal frame,
// returning the buffered result.
func (s *Session) Fetch(ctx context.Context, instr *Instruction) (*FetchResult, error) {
	return CollectEvents(s.FetchEvents(ctx, instr))
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
