package streamio

import (
	"errors"
	"fmt"
	"io"
	"iter"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// errTruncated is the base error for a response file shorter than the
// content length the server declared.
var errTruncated = errors.New("truncated")

// UploadCollect drives an "options-first upload" bidi RPC: it sends every
// frame, half-closes the send side, then feeds each response frame to
// onFrame until the server ends the stream. The server does no work until
// the half-close, so the CloseRequest-before-Receive ordering here is
// load-bearing — this helper is the only place it lives.
func UploadCollect[Req, Resp any](
	stream *connect.BidiStreamForClient[Req, Resp],
	procedure string,
	frames iter.Seq2[*Req, error],
	onFrame func(*Resp) error,
) error {
	defer func() { _ = stream.CloseResponse() }()

	for frame, err := range frames {
		if err != nil {
			_ = stream.CloseRequest()

			return err
		}

		if serr := stream.Send(frame); serr != nil {
			// After a Send failure the definitive status comes from
			// the receive side.
			_ = stream.CloseRequest()
			if _, rerr := stream.Receive(); rerr != nil && !errors.Is(rerr, io.EOF) {
				return transport.WrapError(rerr, procedure)
			}

			return transport.WrapError(serr, procedure)
		}
	}

	if err := stream.CloseRequest(); err != nil {
		return transport.WrapError(err, procedure)
	}

	for {
		resp, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return transport.WrapError(err, procedure)
		}

		if err := onFrame(resp); err != nil {
			return err
		}
	}
}

// FileChunkFrames slices an Upload into ≤MaxChunkBytes FileChunk frames,
// wrapped into the service's request type. An empty upload still yields
// one empty chunk so the payload's name and MIME type reach the server.
func FileChunkFrames[Req any](u rootpullersdk.Upload, wrap func(*commonpb.FileChunk) *Req) iter.Seq2[*Req, error] {
	return func(yield func(*Req, error) bool) {
		buf := make([]byte, MaxChunkBytes)
		sent := false

		for {
			n, err := u.Content.Read(buf)
			if n > 0 {
				chunk := &commonpb.FileChunk{
					Name:          u.Name,
					MimeType:      u.MIMEType,
					ContentLength: protoconv.ClampInt32(u.Size),
					Data:          buf[:n:n],
				}
				// Send marshals before returning, so buf is reusable.
				if !yield(wrap(chunk), nil) {
					return
				}

				sent = true
			}

			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}

				yield(nil, fmt.Errorf("rootpuller: reading upload %q: %w", u.Name, err))

				return
			}
		}

		if !sent {
			yield(wrap(&commonpb.FileChunk{Name: u.Name, MimeType: u.MIMEType}), nil)
		}
	}
}

// Frames chains an opening frame (typically the params message) with any
// number of frame sequences.
func Frames[Req any](head *Req, tails ...iter.Seq2[*Req, error]) iter.Seq2[*Req, error] {
	return func(yield func(*Req, error) bool) {
		if !yield(head, nil) {
			return
		}

		for _, tail := range tails {
			for frame, err := range tail {
				if !yield(frame, err) {
					return
				}
			}
		}
	}
}

// FileCollector reassembles response FileChunk frames into whole files,
// demultiplexed by chunk name (the painter services stream several files
// over one arm). Insertion order is preserved.
type FileCollector struct {
	order []string
	files map[string]*collectedFile
}

type collectedFile struct {
	mimeType string
	expected int
	data     []byte
}

// Add appends one response chunk.
func (fc *FileCollector) Add(chunk *commonpb.FileChunk) {
	if fc.files == nil {
		fc.files = map[string]*collectedFile{}
	}

	name := chunk.GetName()

	f, ok := fc.files[name]
	if !ok {
		f = &collectedFile{}
		fc.files[name] = f
		fc.order = append(fc.order, name)
	}

	if f.mimeType == "" {
		f.mimeType = chunk.GetMimeType()
	}

	if f.expected == 0 {
		f.expected = int(chunk.GetContentLength())
	}

	f.data = append(f.data, chunk.GetData()...)
}

// Files returns the reassembled files in arrival order, validating each
// against the content_length the server declared on its chunks.
func (fc *FileCollector) Files() ([]rootpullersdk.File, error) {
	out := make([]rootpullersdk.File, 0, len(fc.order))
	for _, name := range fc.order {
		f := fc.files[name]
		if f.expected > 0 && len(f.data) != f.expected {
			return nil, fmt.Errorf("rootpuller: file %q %w: got %d bytes, server declared %d", name, errTruncated, len(f.data), f.expected)
		}

		out = append(out, rootpullersdk.File{Name: name, MIMEType: f.mimeType, Data: f.data})
	}

	return out, nil
}
