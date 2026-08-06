// Package completion wraps
// com.entwico.rootpuller.completion.CompletionService: LLM completion via
// Anthropic, Gemini, OpenAI, Ollama, or LiteLLM, with optional streamed
// file attachments.
package completion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	completionpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/completion"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/completion/completionconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// MaxUploadBytes is the server's cap on the total bytes streamed across
// all attachments of one CompleteWithAttachments call.
const MaxUploadBytes = 64 << 20

// Service calls the CompletionService.
type Service struct {
	rpc             completionconnect.CompletionServiceClient
	defaultProvider Provider
	defaultModel    string
}

// Option configures a Service at construction.
type Option func(*Service)

// WithDefaultProvider sets the LLM provider used when a call's Options
// leave Provider empty.
func WithDefaultProvider(provider Provider) Option {
	return func(s *Service) { s.defaultProvider = provider }
}

// WithDefaultModel sets the model used when a call's Options leave Model
// empty.
func WithDefaultModel(model string) Option {
	return func(s *Service) { s.defaultModel = model }
}

// NewService builds a CompletionService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: completionconnect.NewCompletionServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Options tunes a Complete or CompleteWithAttachments call. Nil keeps
// all defaults.
type Options struct {
	// Provider overrides the service default LLM provider.
	Provider Provider
	// Model overrides the service default model; empty picks the
	// provider's default.
	Model string
	// System is the system prompt (empty = none).
	System string
	// ResponseSchema is a JSON schema enforcing structured output
	// (empty = free-form).
	ResponseSchema string
	// Temperature overrides the provider default sampling temperature.
	// Zero is a meaningful value, hence the pointer; nil keeps the
	// default.
	Temperature *float32
	// MaxTokens caps the output length (0 = provider default).
	MaxTokens int
	// ThinkingEffort controls extended-thinking depth.
	ThinkingEffort ThinkingEffort
	// ThinkingBudgetTokens caps extended thinking for providers that
	// meter it (0 = provider default).
	ThinkingBudgetTokens int
}

// Attachment is a file streamed alongside a CompleteWithAttachments
// call. ID must match an AttachmentPart in one of the messages.
type Attachment struct {
	ID      string
	Content io.Reader
}

// Complete calls CompletionService/Complete. Files must be inline
// FilePart values; AttachmentPart references are rejected by the server —
// use CompleteWithAttachments for those.
func (s *Service) Complete(ctx context.Context, messages []Message, opts *Options) (*Response, error) {
	msg, err := s.buildRequest(messages, opts)
	if err != nil {
		return nil, err
	}

	resp, err := s.rpc.Complete(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, completionconnect.CompletionServiceCompleteProcedure)
	}

	return responseFromProto(resp.Msg), nil
}

// CompleteWithAttachments calls CompletionService/CompleteUpload: the
// request goes out first, then each attachment's content is streamed in
// chunks. Every AttachmentPart in the messages must have a matching
// Attachment (and vice versa); the total payload is capped at
// MaxUploadBytes.
func (s *Service) CompleteWithAttachments(ctx context.Context, messages []Message, attachments []Attachment, opts *Options) (*Response, error) {
	msg, err := s.buildRequest(messages, opts)
	if err != nil {
		return nil, err
	}

	if err := checkAttachments(messages, attachments); err != nil {
		return nil, err
	}

	procedure := completionconnect.CompletionServiceCompleteUploadProcedure

	stream := s.rpc.CompleteUpload(ctx)
	if err := stream.Send(&completionpb.CompleteUploadRequest{
		Frame: &completionpb.CompleteUploadRequest_Request{Request: msg},
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

				chunk := &completionpb.CompleteUploadRequest_AttachmentChunk{
					AttachmentId: att.ID,
					Data:         buf[:n:n],
				}
				// Send marshals before returning, so buf is reusable.
				if serr := stream.Send(&completionpb.CompleteUploadRequest{
					Frame: &completionpb.CompleteUploadRequest_Chunk{Chunk: chunk},
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
			if serr := stream.Send(&completionpb.CompleteUploadRequest{
				Frame: &completionpb.CompleteUploadRequest_Chunk{Chunk: &completionpb.CompleteUploadRequest_AttachmentChunk{AttachmentId: att.ID}},
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

// buildRequest resolves the service defaults into opts and converts the
// call to the wire request.
func (s *Service) buildRequest(messages []Message, opts *Options) (*completionpb.CompleteRequest, error) {
	if opts == nil {
		opts = &Options{}
	}

	providerName := opts.Provider
	if providerName == ProviderDefault {
		providerName = s.defaultProvider
	}

	provider, ok := providerToProto[providerName]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown provider %q", providerName))
	}

	effort, ok := thinkingEffortToProto[opts.ThinkingEffort]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown thinking effort %q", opts.ThinkingEffort))
	}

	msg := &completionpb.CompleteRequest{
		Provider:    provider,
		Temperature: opts.Temperature,
	}

	model := opts.Model
	if model == "" {
		model = s.defaultModel
	}

	if model != "" {
		msg.Model = &model
	}

	if opts.System != "" {
		msg.System = &opts.System
	}

	if opts.ResponseSchema != "" {
		msg.ResponseSchema = &opts.ResponseSchema
	}

	if opts.MaxTokens > 0 {
		msg.MaxTokens = protoconv.Int32Ptr(&opts.MaxTokens)
	}

	if opts.ThinkingBudgetTokens > 0 {
		msg.ThinkingBudgetTokens = protoconv.Int32Ptr(&opts.ThinkingBudgetTokens)
	}

	if effort != completionpb.ThinkingEffort_THINKING_EFFORT_UNSPECIFIED {
		msg.ThinkingEffort = &effort
	}

	for _, m := range messages {
		pm, err := m.toProto()
		if err != nil {
			return nil, err
		}

		msg.Messages = append(msg.Messages, pm)
	}

	return msg, nil
}

// JSON calls CompletionService/Complete and unmarshals the response
// content into T. Set opts.ResponseSchema to enforce the shape
// server-side. It is a package-level function because Go methods cannot
// be generic.
func JSON[T any](ctx context.Context, s *Service, messages []Message, opts *Options) (T, *Response, error) {
	var out T

	resp, err := s.Complete(ctx, messages, opts)
	if err != nil {
		return out, nil, err
	}

	if uerr := json.Unmarshal([]byte(resp.Content), &out); uerr != nil {
		return out, resp, fmt.Errorf("unmarshaling completion content (%d bytes): %w", len(resp.Content), uerr)
	}

	return out, resp, nil
}

// checkAttachments verifies the declared AttachmentParts and provided
// Attachments match one-to-one, so mismatches fail locally instead of as
// server-side stream errors.
func checkAttachments(messages []Message, attachments []Attachment) error {
	declared := map[string]bool{}

	for _, m := range messages {
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

func closeAndWrap(stream *connect.ClientStreamForClient[completionpb.CompleteUploadRequest, completionpb.CompleteUploadResponse], err error, procedure string) error {
	// After a Send failure the definitive error comes from
	// CloseAndReceive.
	if _, cerr := stream.CloseAndReceive(); cerr != nil {
		return transport.WrapError(cerr, procedure)
	}

	return transport.WrapError(err, procedure)
}

func responseFromProto(pb *completionpb.CompleteResponse) *Response {
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
