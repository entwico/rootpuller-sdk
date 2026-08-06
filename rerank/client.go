// Package rerank wraps com.entwico.rootpuller.rerank.RerankService:
// cross-encoder relevance scoring of candidate documents against a query
// (second-stage retrieval), plus discovery of the locally-served reranker
// models.
package rerank

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rerankpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank/rerankconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client calls the RerankService. Obtain one from
// rootpuller.Client.Rerank.
type Client struct {
	rpc rerankconnect.RerankServiceClient
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: rerankconnect.NewRerankServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// Rerank calls RerankService/Rerank: scores the request's documents
// against its query and returns them sorted by descending relevance.
func (c *Client) Rerank(ctx context.Context, req *Request) (*Response, error) {
	model, err := req.Model.toProto()
	if err != nil {
		return nil, err
	}
	msg := &rerankpb.RerankRequest{
		Query:     req.Query,
		Documents: req.Documents,
		Model:     model,
	}
	if req.TopN != nil || req.MaxTokens != nil || req.ReturnDocuments {
		msg.Options = &rerankpb.RerankOptions{
			TopN:            protoconv.Int32Ptr(req.TopN),
			MaxTokens:       protoconv.Int32Ptr(req.MaxTokens),
			ReturnDocuments: req.ReturnDocuments,
		}
	}
	resp, err := c.rpc.Rerank(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, rerankconnect.RerankServiceRerankProcedure)
	}
	results := resp.Msg.GetResults()
	out := &Response{
		Results: make([]Result, len(results)),
		Model:   modelRefFromProto(resp.Msg.GetModel()),
	}
	for i, r := range results {
		out.Results[i] = Result{
			Index:          int(r.GetIndex()),
			RelevanceScore: r.GetRelevanceScore(),
			Document:       r.GetDocument(),
		}
	}
	return out, nil
}

// ListModels calls RerankService/ListModels: lists the reranker models
// available on the server.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	resp, err := c.rpc.ListModels(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, rerankconnect.RerankServiceListModelsProcedure)
	}
	models := resp.Msg.GetModels()
	out := make([]ModelInfo, len(models))
	for i, m := range models {
		out[i] = ModelInfo{
			Model:       modelRefFromProto(m.GetModel()),
			MaxTokens:   int(m.GetMaxTokens()),
			Loaded:      m.GetLoaded(),
			Description: m.GetDescription(),
		}
	}
	return out, nil
}
