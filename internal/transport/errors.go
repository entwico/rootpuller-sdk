package transport

import (
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/entwico/rootpuller-sdk/apierror"
)

// WrapError converts any RPC failure into *apierror.Error, extracting the
// google.rpc.RetryInfo hint when the server attached one. It is called at
// every Send/Receive/unary call site; nil stays nil so call sites can wrap
// unconditionally.
func WrapError(err error, procedure string) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*apierror.Error](err); ok {
		return err // already wrapped (e.g. local validation error)
	}
	var retryAfter time.Duration
	message := err.Error()
	if ce, ok := errors.AsType[*connect.Error](err); ok {
		message = ce.Message()
		for _, detail := range ce.Details() {
			value, verr := detail.Value()
			if verr != nil {
				continue
			}
			if info, ok := value.(*errdetails.RetryInfo); ok && info.GetRetryDelay() != nil {
				retryAfter = info.GetRetryDelay().AsDuration()
			}
		}
	}
	return apierror.New(connect.CodeOf(err), message, procedure, retryAfter, err)
}
