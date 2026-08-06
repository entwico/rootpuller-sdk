// Package embedding wraps
// com.entwico.rootpuller.embedding.VectorEmbeddingService: batch and
// streamed text embedding against local ONNX/fastembed models or cloud
// embedding APIs, plus model discovery.
package embedding

import (
	"context"
	"iter"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	embeddingpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/embedding"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/embedding/embeddingconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client calls the VectorEmbeddingService. Obtain one from
// rootpuller.Client.Embedding.
type Client struct {
	rpc embeddingconnect.VectorEmbeddingServiceClient
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: embeddingconnect.NewVectorEmbeddingServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// Embed calls VectorEmbeddingService/Embed: embeds all inputs in one
// round trip. For large ingestion batches where results should arrive
// incrementally, use EmbedStream.
func (c *Client) Embed(ctx context.Context, req *Request) (*Response, error) {
	msg, err := req.toProto()
	if err != nil {
		return nil, err
	}
	resp, err := c.rpc.Embed(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, embeddingconnect.VectorEmbeddingServiceEmbedProcedure)
	}
	pb := resp.Msg
	embeddings := make([]Embedding, len(pb.GetResults()))
	for i, r := range pb.GetResults() {
		embeddings[i] = embeddingFromProto(r)
	}
	return &Response{
		Embeddings:     embeddings,
		Model:          modelRefFromProto(pb.GetModel()),
		Mode:           enumFromProto(modeFromProto, pb.GetMode()),
		Task:           enumFromProto(taskFromProto, pb.GetTask()),
		DenseDimension: int(pb.GetDenseDimension()),
		Usage:          protoconv.FromProtoUsage(pb.GetUsage()),
	}, nil
}

// EmbedStream calls VectorEmbeddingService/EmbedStream: one StreamResult
// per input, yielded in request order as the server produces them.
// Iteration stops on the first error; breaking out of the loop closes the
// stream.
func (c *Client) EmbedStream(ctx context.Context, req *Request) iter.Seq2[StreamResult, error] {
	msg, err := req.toProto()
	if err != nil {
		return func(yield func(StreamResult, error) bool) {
			yield(StreamResult{}, err)
		}
	}
	stream, serr := c.rpc.EmbedStream(ctx, connect.NewRequest(msg))
	if serr != nil {
		wrapped := transport.WrapError(serr, embeddingconnect.VectorEmbeddingServiceEmbedStreamProcedure)
		return func(yield func(StreamResult, error) bool) {
			yield(StreamResult{}, wrapped)
		}
	}
	return streamio.EventSeq(stream, embeddingconnect.VectorEmbeddingServiceEmbedStreamProcedure, false,
		func(pb *embeddingpb.EmbedResponse) (StreamResult, bool, error) {
			result := StreamResult{
				Model:          modelRefFromProto(pb.GetModel()),
				Mode:           enumFromProto(modeFromProto, pb.GetMode()),
				Task:           enumFromProto(taskFromProto, pb.GetTask()),
				DenseDimension: int(pb.GetDenseDimension()),
				Usage:          protoconv.FromProtoUsage(pb.GetUsage()),
			}
			if results := pb.GetResults(); len(results) > 0 {
				result.Embedding = embeddingFromProto(results[0])
			}
			return result, false, nil
		})
}

// ListModels calls VectorEmbeddingService/ListModels: the embedding
// models available on this server.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	resp, err := c.rpc.ListModels(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, embeddingconnect.VectorEmbeddingServiceListModelsProcedure)
	}
	models := make([]ModelInfo, len(resp.Msg.GetModels()))
	for i, m := range resp.Msg.GetModels() {
		models[i] = modelInfoFromProto(m)
	}
	return models, nil
}
