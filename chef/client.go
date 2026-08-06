// Package chef wraps com.entwico.rootpuller.chef.DocumentProcessingService:
// text cleanup, table extraction, and markdown decomposition into tables,
// code blocks, images, and chunks, plus tokenizer discovery.
package chef

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	chefpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chef"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chef/chefconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the DocumentProcessingService.
type Service struct {
	rpc        chefconnect.DocumentProcessingServiceClient
	deployment string
}

// Option configures a Service at construction.
type Option func(*Service)

// WithDeployment sends the rootpuller-deployment routing header (e.g.
// "local", "cloudrun") on every call. A per-call
// rootpullersdk.ContextWithDeployment value still wins.
func WithDeployment(name string) Option {
	return func(s *Service) { s.deployment = name }
}

// NewService builds a DocumentProcessingService client on the sdk
// connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: chefconnect.NewDocumentProcessingServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// ProcessText calls DocumentProcessingService/ProcessText: cleans up raw
// text and assigns it a document ID.
func (s *Service) ProcessText(ctx context.Context, text string) (*TextResult, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	resp, err := s.rpc.ProcessText(ctx, connect.NewRequest(&chefpb.ProcessTextRequest{Text: text}))
	if err != nil {
		return nil, transport.WrapError(err, chefconnect.DocumentProcessingServiceProcessTextProcedure)
	}

	return &TextResult{
		Content:  resp.Msg.GetContent(),
		ID:       resp.Msg.GetId(),
		Metadata: fromProtoMetadata(resp.Msg.GetMetadata()),
	}, nil
}

// TableOptions tunes a ProcessTable call. Nil keeps all defaults.
type TableOptions struct {
	// SourceType selects the input format; the zero value keeps the
	// server default (markdown).
	SourceType TableSourceType
}

// ProcessTable calls DocumentProcessingService/ProcessTable: extracts the
// tables found in content and renders each as markdown.
func (s *Service) ProcessTable(ctx context.Context, content string, opts *TableOptions) ([]MarkdownTable, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	if opts == nil {
		opts = &TableOptions{}
	}

	st, err := opts.SourceType.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chefpb.ProcessTableRequest{Content: content, SourceType: st}

	resp, err := s.rpc.ProcessTable(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, chefconnect.DocumentProcessingServiceProcessTableProcedure)
	}

	return fromProtoTables(resp.Msg.GetTables()), nil
}

// MarkdownOptions tunes a ProcessMarkdown call. Nil keeps all defaults.
type MarkdownOptions struct {
	// Tokenizer selects the tokenizer for chunk token counting; "" keeps
	// the server default (character). See Service.ListTokenizers.
	Tokenizer string
}

// ProcessMarkdown calls DocumentProcessingService/ProcessMarkdown:
// decomposes a markdown document into tables, code blocks, images, and
// text chunks.
func (s *Service) ProcessMarkdown(ctx context.Context, text string, opts *MarkdownOptions) (*MarkdownResult, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	if opts == nil {
		opts = &MarkdownOptions{}
	}

	msg := &chefpb.ProcessMarkdownRequest{Text: text}
	if opts.Tokenizer != "" {
		msg.Tokenizer = &opts.Tokenizer
	}

	resp, err := s.rpc.ProcessMarkdown(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, chefconnect.DocumentProcessingServiceProcessMarkdownProcedure)
	}

	return &MarkdownResult{
		Content:    resp.Msg.GetContent(),
		ID:         resp.Msg.GetId(),
		Tables:     fromProtoTables(resp.Msg.GetTables()),
		CodeBlocks: fromProtoCodeBlocks(resp.Msg.GetCodeBlocks()),
		Images:     fromProtoImages(resp.Msg.GetImages()),
		Chunks:     protoconv.FromProtoTextChunks(resp.Msg.GetChunks()),
		Metadata:   fromProtoMetadata(resp.Msg.GetMetadata()),
	}, nil
}

// ListTokenizers calls DocumentProcessingService/ListTokenizers: reports
// the tokenizers available for markdown chunk token counting, for
// client-side discovery of MarkdownOptions.Tokenizer values.
func (s *Service) ListTokenizers(ctx context.Context) ([]TokenizerInfo, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	resp, err := s.rpc.ListTokenizers(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, chefconnect.DocumentProcessingServiceListTokenizersProcedure)
	}

	return fromProtoTokenizers(resp.Msg.GetTokenizers()), nil
}
