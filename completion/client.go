// Package completion wraps
// com.entwico.rootpuller.completion.CompletionService: LLM completion via
// Anthropic, Gemini, OpenAI, Ollama, or LiteLLM, with optional streamed
// file attachments.
package completion

import (
	"context"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/completion"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/completion/completionconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// MaxUploadBytes is the server's cap on the total bytes streamed across
// all attachments of one CompleteWithAttachments call.
const MaxUploadBytes = 64 << 20

// Client calls the CompletionService. Obtain one from
// rootpuller.Client.Completion.
type Client struct {
	rpc completionconnect.CompletionServiceClient
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: completionconnect.NewCompletionServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// Attachment is a file streamed alongside a CompleteWithAttachments
// request. ID must match an AttachmentPart in one of the messages.
type Attachment struct {
	ID      string
	Content io.Reader
}

// Complete calls CompletionService/Complete. Files must be inline
// FilePart values; AttachmentPart references are rejected by the server —
// use CompleteWithAttachments for those.
func (c *Client) Complete(ctx context.Context, req *Request) (*Response, error) {
	msg, err := req.toProto()
	if err != nil {
		return nil, err
	}
	resp, err := c.rpc.Complete(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, completionconnect.CompletionServiceCompleteProcedure)
	}
	return responseFromProto(resp.Msg), nil
}

// CompleteWithAttachments calls CompletionService/CompleteUpload: the
// request goes out first, then each attachment's content is streamed in
// chunks. Every AttachmentPart in the request must have a matching
// Attachment (and vice versa); the total payload is capped at
// MaxUploadBytes.
func (c *Client) CompleteWithAttachments(ctx context.Context, req *Request, attachments ...Attachment) (*Response, error) {
	msg, err := req.toProto()
	if err != nil {
		return nil, err
	}
	if err := checkAttachments(req, attachments); err != nil {
		return nil, err
	}

	procedure := completionconnect.CompletionServiceCompleteUploadProcedure
	stream := c.rpc.CompleteUpload(ctx)
	if err := stream.Send(&completion.CompleteUploadRequest{
		Frame: &completion.CompleteUploadRequest_Request{Request: msg},
	}); err != nil {
		return nil, closeAndWrap(stream, err, procedure)
	}

	var total int64
	for _, att := range attachments {
		sentAny := false
		buf := make([]byte, streamio.MaxChunkBytes)
		for {
			n, rerr := att.Content.Read(buf)
			if n > 0 {
				total += int64(n)
				if total > MaxUploadBytes {
					_, _ = stream.CloseAndReceive()
					return nil, invalidArgument(fmt.Sprintf("attachments exceed the %d byte upload limit", MaxUploadBytes))
				}
				chunk := &completion.CompleteUploadRequest_AttachmentChunk{
					AttachmentId: att.ID,
					Data:         buf[:n:n],
				}
				// Send marshals before returning, so buf is reusable.
				if serr := stream.Send(&completion.CompleteUploadRequest{
					Frame: &completion.CompleteUploadRequest_Chunk{Chunk: chunk},
				}); serr != nil {
					return nil, closeAndWrap(stream, serr, procedure)
				}
				sentAny = true
			}
			if rerr != nil {
				if errors.Is(rerr, io.EOF) {
					break
				}
				return nil, invalidArgument(fmt.Sprintf("reading attachment %q: %v", att.ID, rerr))
			}
		}
		if !sentAny {
			// The server requires at least one (possibly empty) chunk
			// per declared attachment.
			if serr := stream.Send(&completion.CompleteUploadRequest{
				Frame: &completion.CompleteUploadRequest_Chunk{Chunk: &completion.CompleteUploadRequest_AttachmentChunk{AttachmentId: att.ID}},
			}); serr != nil {
				return nil, closeAndWrap(stream, serr, procedure)
			}
		}
	}

	resp, err := stream.CloseAndReceive()
	if err != nil {
		return nil, transport.WrapError(err, procedure)
	}
	return responseFromProto(resp.Msg.GetResponse()), nil
}

// checkAttachments verifies the declared AttachmentParts and provided
// Attachments match one-to-one, so mismatches fail locally instead of as
// server-side stream errors.
func checkAttachments(req *Request, attachments []Attachment) error {
	declared := map[string]bool{}
	for _, m := range req.Messages {
		for _, part := range m.Parts {
			if ap, ok := part.(AttachmentPart); ok {
				declared[ap.ID] = true
			}
		}
	}
	provided := map[string]bool{}
	for _, att := range attachments {
		if att.ID == "" {
			return invalidArgument("attachment with empty ID")
		}
		if !declared[att.ID] {
			return invalidArgument(fmt.Sprintf("attachment %q has no matching AttachmentPart in the request", att.ID))
		}
		provided[att.ID] = true
	}
	for id := range declared {
		if !provided[id] {
			return invalidArgument(fmt.Sprintf("AttachmentPart %q has no matching attachment", id))
		}
	}
	return nil
}

func closeAndWrap(stream *connect.ClientStreamForClient[completion.CompleteUploadRequest, completion.CompleteUploadResponse], err error, procedure string) error {
	// After a Send failure the definitive error comes from
	// CloseAndReceive.
	if _, cerr := stream.CloseAndReceive(); cerr != nil {
		return transport.WrapError(cerr, procedure)
	}
	return transport.WrapError(err, procedure)
}

func responseFromProto(pb *completion.CompleteResponse) *Response {
	if pb == nil {
		return &Response{}
	}
	return &Response{
		Content:  pb.GetContent(),
		Model:    pb.GetModel(),
		Usage:    protoconv.FromProtoUsage(pb.GetUsage()),
		Thinking: pb.GetThinking(),
	}
}
