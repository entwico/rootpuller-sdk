// Package streamio implements the wire choreography shared by the SDK's
// streaming RPCs: server-stream iteration, options-first chunked uploads,
// and file/matrix chunk collection. All CloseRequest/CloseSend ordering
// hazards live here, in exactly one place.
package streamio

import (
	"iter"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// EventSeq adapts a connect server stream into a Go iterator. convert maps
// each wire frame to a facade value and reports whether the frame is the
// protocol's terminal frame; a convert error is yielded as the iteration
// error and ends the stream. When requireTerminal is set (the
// webcontent/scrape protocols), reaching EOF without a terminal frame
// yields apierror.ErrMissingTerminal. Breaking out of the loop closes the
// stream.
func EventSeq[Resp, E any](
	stream *connect.ServerStreamForClient[Resp],
	procedure string,
	requireTerminal bool,
	convert func(*Resp) (E, bool, error),
) iter.Seq2[E, error] {
	return func(yield func(E, error) bool) {
		defer func() { _ = stream.Close() }()
		var zero E
		sawTerminal := false
		for stream.Receive() {
			event, terminal, err := convert(stream.Msg())
			if err != nil {
				yield(zero, err)
				return
			}
			if terminal {
				sawTerminal = true
			}
			if !yield(event, nil) {
				return
			}
		}
		if err := stream.Err(); err != nil {
			yield(zero, transport.WrapError(err, procedure))
			return
		}
		if requireTerminal && !sawTerminal {
			yield(zero, apierror.ErrMissingTerminal)
		}
	}
}
