package rootpullertest

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"
)

// Static protocol-violation errors shared by the fake handlers. The fakes
// enforce the wire protocol strictly and report violations as these
// sentinels wrapped in InvalidArgument connect errors, so SDK regressions
// surface as test failures.
var (
	errDuplicateParamsFrame = errors.New("duplicate params frame")
	errDuplicateHeaderFrame = errors.New("duplicate header frame")
	errFrameBeforeParams    = errors.New("frame before params")
	errFrameBeforeHeader    = errors.New("frame before header")
	errChunkTooLarge        = errors.New("chunk exceeds 2 MiB")
	errUnexpectedVariant    = errors.New("unexpected request variant")
	errMissingParams        = errors.New("missing params")
	errMissingHeader        = errors.New("missing header")
)

// invalidArgument wraps a protocol-violation error the way the real server
// reports it.
func invalidArgument(err error) *connect.Error {
	return connect.NewError(connect.CodeInvalidArgument, err)
}

// errBeforeParams reports a data frame ("file", "input_image", ...)
// arriving before the params frame.
func errBeforeParams(frame string) *connect.Error {
	return invalidArgument(fmt.Errorf("%s %w", frame, errFrameBeforeParams))
}

// errBeforeHeader reports a data frame ("points", "supervised_labels")
// arriving before the header frame.
func errBeforeHeader(frame string) *connect.Error {
	return invalidArgument(fmt.Errorf("%s %w", frame, errFrameBeforeHeader))
}
