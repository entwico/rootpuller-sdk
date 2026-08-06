// Package chef wraps com.entwico.rootpuller.chef.DocumentProcessingService:
// text cleanup, table extraction, and markdown decomposition into tables,
// code blocks, images, and chunks, plus tokenizer discovery.
package chef

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	chefpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chef"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chef/chefconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client calls the DocumentProcessingService. Obtain one from
// rootpuller.Client.Chef.
type Client struct {
	rpc chefconnect.DocumentProcessingServiceClient
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: chefconnect.NewDocumentProcessingServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// ProcessText calls DocumentProcessingService/ProcessText: cleans up raw
// text and assigns it a document ID.
func (c *Client) ProcessText(ctx context.Context, text string) (*TextResult, error) {
	resp, err := c.rpc.ProcessText(ctx, connect.NewRequest(&chefpb.ProcessTextRequest{Text: text}))
	if err != nil {
		return nil, transport.WrapError(err, chefconnect.DocumentProcessingServiceProcessTextProcedure)
	}
	return &TextResult{
		Content:  resp.Msg.GetContent(),
		ID:       resp.Msg.GetId(),
		Metadata: fromProtoMetadata(resp.Msg.GetMetadata()),
	}, nil
}

// ProcessTable calls DocumentProcessingService/ProcessTable: extracts the
// tables found in content and renders each as markdown. The zero
// sourceType keeps the server default (markdown).
func (c *Client) ProcessTable(ctx context.Context, content string, sourceType TableSourceType) ([]MarkdownTable, error) {
	st, err := sourceType.toProto()
	if err != nil {
		return nil, err
	}
	msg := &chefpb.ProcessTableRequest{Content: content, SourceType: st}
	resp, err := c.rpc.ProcessTable(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, chefconnect.DocumentProcessingServiceProcessTableProcedure)
	}
	return fromProtoTables(resp.Msg.GetTables()), nil
}

// ProcessMarkdown calls DocumentProcessingService/ProcessMarkdown:
// decomposes a markdown document into tables, code blocks, images, and
// text chunks.
func (c *Client) ProcessMarkdown(ctx context.Context, req *MarkdownRequest) (*MarkdownResult, error) {
	msg := &chefpb.ProcessMarkdownRequest{Text: req.Text, Tokenizer: req.Tokenizer}
	resp, err := c.rpc.ProcessMarkdown(ctx, connect.NewRequest(msg))
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
// client-side discovery of MarkdownRequest.Tokenizer values.
func (c *Client) ListTokenizers(ctx context.Context) ([]TokenizerInfo, error) {
	resp, err := c.rpc.ListTokenizers(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, chefconnect.DocumentProcessingServiceListTokenizersProcedure)
	}
	return fromProtoTokenizers(resp.Msg.GetTokenizers()), nil
}
