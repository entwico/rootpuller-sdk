package rootpullersdk

import (
	"github.com/entwico/rootpuller-sdk/internal/apierr"
)

// Error is the typed error returned by every SDK call that fails at the
// transport or server level. Match broad classes with errors.Is against
// the Err* sentinels, or errors.As to inspect the code, procedure, and
// the server's retry hint.
type Error = apierr.Error

// Sentinels for the status codes consumers most commonly branch on. They
// match any *Error carrying the same code via errors.Is.
var (
	ErrUnauthenticated   = apierr.ErrUnauthenticated
	ErrInvalidArgument   = apierr.ErrInvalidArgument
	ErrNotFound          = apierr.ErrNotFound
	ErrResourceExhausted = apierr.ErrResourceExhausted
	ErrUnimplemented     = apierr.ErrUnimplemented
	ErrUnavailable       = apierr.ErrUnavailable
	ErrDeadlineExceeded  = apierr.ErrDeadlineExceeded
	ErrCanceled          = apierr.ErrCanceled
	ErrAborted           = apierr.ErrAborted
	ErrInternal          = apierr.ErrInternal
)

// ErrMissingTerminal reports a webcontent/scrape event stream that ended
// without the protocol's mandatory done or error terminal frame.
var ErrMissingTerminal = apierr.ErrMissingTerminal

// IsTransient reports whether err is a failure that is typically worth
// retrying: server backpressure, temporary unavailability, deadline
// expiry, or an aborted operation.
func IsTransient(err error) bool { return apierr.IsTransient(err) }
