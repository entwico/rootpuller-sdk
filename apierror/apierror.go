// Package apierror defines the error model shared by every rootpuller-sdk
// service package. All RPC failures surface as *Error; match broad classes
// with errors.Is against the exported sentinels, or errors.As to inspect
// the full error.
package apierror

import (
	"errors"
	"time"

	"connectrpc.com/connect"
)

// Error is the typed error returned by every SDK call that fails at the
// transport or server level.
type Error struct {
	// Code is the canonical gRPC status code of the failure.
	Code connect.Code
	// Message is the server-provided (or transport-provided) description.
	Message string
	// Procedure is the full RPC name the failure occurred on, e.g.
	// "/com.entwico.rootpuller.chunker.TextChunkerService/ChunkSemantic".
	Procedure string
	// RetryAfter is the server's retry hint (google.rpc.RetryInfo) for
	// backpressure responses such as ResourceExhausted. Zero when absent.
	RetryAfter time.Duration

	cause error
}

// New builds an *Error. The cause (typically the underlying *connect.Error)
// is reachable via Unwrap.
func New(code connect.Code, message, procedure string, retryAfter time.Duration, cause error) *Error {
	return &Error{Code: code, Message: message, Procedure: procedure, RetryAfter: retryAfter, cause: cause}
}

func (e *Error) Error() string {
	msg := "rootpuller: " + e.Code.String()
	if e.Procedure != "" {
		msg += " calling " + e.Procedure
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

func (e *Error) Unwrap() error { return e.cause }

// Is reports whether target is one of the package sentinels with the same
// code, enabling errors.Is(err, apierror.ErrUnauthenticated).
func (e *Error) Is(target error) bool {
	s, ok := target.(*sentinelError)
	return ok && e.Code == s.code
}

type sentinelError struct{ code connect.Code }

func (s *sentinelError) Error() string { return "rootpuller: " + s.code.String() }

// Sentinels for the status codes consumers most commonly branch on.
// They match any *Error carrying the same code.
var (
	ErrUnauthenticated   error = &sentinelError{connect.CodeUnauthenticated}
	ErrInvalidArgument   error = &sentinelError{connect.CodeInvalidArgument}
	ErrNotFound          error = &sentinelError{connect.CodeNotFound}
	ErrResourceExhausted error = &sentinelError{connect.CodeResourceExhausted}
	ErrUnimplemented     error = &sentinelError{connect.CodeUnimplemented}
	ErrUnavailable       error = &sentinelError{connect.CodeUnavailable}
	ErrDeadlineExceeded  error = &sentinelError{connect.CodeDeadlineExceeded}
	ErrCanceled          error = &sentinelError{connect.CodeCanceled}
)

// ErrMissingTerminal reports a webcontent/scrape event stream that ended
// without the protocol's mandatory done or error terminal frame. Treat it
// as a server or transport bug, not a normal completion.
var ErrMissingTerminal = errors.New("rootpuller: stream ended without done/error terminal frame")
