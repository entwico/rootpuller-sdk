// Package chunker wraps com.entwico.rootpuller.chunker.TextChunkerService:
// semantic, token, sentence, code, recursive, late, neural, and slumber
// text chunking (Chonkie). All methods return one []common.TextChunk per
// input text, row-aligned with the request's Texts slice.
package chunker

import (
	"context"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/common"
	chunkerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker/chunkerconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client calls the TextChunkerService. Obtain one from
// rootpuller.Client.Chunker.
type Client struct {
	rpc        chunkerconnect.TextChunkerServiceClient
	deployment string
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: chunkerconnect.NewTextChunkerServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// WithDeployment returns a client that sends the rootpuller-deployment
// routing header (e.g. "local", "cloudrun") on every call. A per-call
// rootpuller.ContextWithDeployment value still wins.
func (c *Client) WithDeployment(name string) *Client {
	derived := *c
	derived.deployment = name

	return &derived
}

// ChunkSemantic calls TextChunkerService/ChunkSemantic: embedding-driven
// splitting at semantic similarity boundaries.
func (c *Client) ChunkSemantic(ctx context.Context, req *SemanticRequest) ([][]common.TextChunk, error) {
	inclusion, err := req.DelimiterInclusion.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkSemanticRequest{
		Texts:                    req.Texts,
		Model:                    req.Model,
		Threshold:                req.Threshold,
		ChunkSize:                protoconv.Int32Ptr(req.ChunkSize),
		SimilarityWindow:         protoconv.Int32Ptr(req.SimilarityWindow),
		MinSentencesPerChunk:     protoconv.Int32Ptr(req.MinSentencesPerChunk),
		MinCharactersPerSentence: protoconv.Int32Ptr(req.MinCharactersPerSentence),
		Delimiters:               req.Delimiters,
		DelimiterInclusion:       inclusion,
		SkipWindow:               protoconv.Int32Ptr(req.SkipWindow),
		FilterWindow:             protoconv.Int32Ptr(req.FilterWindow),
		FilterPolyorder:          protoconv.Int32Ptr(req.FilterPolyorder),
		FilterTolerance:          req.FilterTolerance,
		Normalize:                protoconv.ToProtoNormalize(req.Normalize),
		MinCharactersPerChunk:    protoconv.Int32Ptr(req.MinCharactersPerChunk),
	}

	return c.call(ctx, chunkerconnect.TextChunkerServiceChunkSemanticProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return c.rpc.ChunkSemantic(ctx, connect.NewRequest(msg))
		})
}

// ChunkToken calls TextChunkerService/ChunkToken: fixed-size token
// windows with optional overlap.
func (c *Client) ChunkToken(ctx context.Context, req *TokenRequest) ([][]common.TextChunk, error) {
	tokenizer, err := req.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkTokenRequest{
		Texts:        req.Texts,
		Tokenizer:    tokenizer,
		ChunkSize:    protoconv.Int32Ptr(req.ChunkSize),
		ChunkOverlap: protoconv.Int32Ptr(req.ChunkOverlap),
		Normalize:    protoconv.ToProtoNormalize(req.Normalize),
	}

	return c.call(ctx, chunkerconnect.TextChunkerServiceChunkTokenProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return c.rpc.ChunkToken(ctx, connect.NewRequest(msg))
		})
}

// ChunkSentence calls TextChunkerService/ChunkSentence: sentence-boundary
// chunking with token budgets.
func (c *Client) ChunkSentence(ctx context.Context, req *SentenceRequest) ([][]common.TextChunk, error) {
	tokenizer, err := req.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	inclusion, err := req.DelimiterInclusion.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkSentenceRequest{
		Texts:                    req.Texts,
		Tokenizer:                tokenizer,
		ChunkSize:                protoconv.Int32Ptr(req.ChunkSize),
		ChunkOverlap:             protoconv.Int32Ptr(req.ChunkOverlap),
		MinSentencesPerChunk:     protoconv.Int32Ptr(req.MinSentencesPerChunk),
		MinCharactersPerSentence: protoconv.Int32Ptr(req.MinCharactersPerSentence),
		Approximate:              req.Approximate,
		Delimiters:               req.Delimiters,
		DelimiterInclusion:       inclusion,
		Normalize:                protoconv.ToProtoNormalize(req.Normalize),
	}

	return c.call(ctx, chunkerconnect.TextChunkerServiceChunkSentenceProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return c.rpc.ChunkSentence(ctx, connect.NewRequest(msg))
		})
}

// ChunkCode calls TextChunkerService/ChunkCode: syntax-aware chunking of
// source code.
func (c *Client) ChunkCode(ctx context.Context, req *CodeRequest) ([][]common.TextChunk, error) {
	tokenizer, err := req.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkCodeRequest{
		Texts:     req.Texts,
		Tokenizer: tokenizer,
		ChunkSize: protoconv.Int32Ptr(req.ChunkSize),
		Language:  req.Language,
	}

	return c.call(ctx, chunkerconnect.TextChunkerServiceChunkCodeProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return c.rpc.ChunkCode(ctx, connect.NewRequest(msg))
		})
}

// ChunkRecursive calls TextChunkerService/ChunkRecursive: hierarchical
// splitting by recipe-defined separators.
func (c *Client) ChunkRecursive(ctx context.Context, req *RecursiveRequest) ([][]common.TextChunk, error) {
	tokenizer, err := req.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkRecursiveRequest{
		Texts:                 req.Texts,
		Tokenizer:             tokenizer,
		ChunkSize:             protoconv.Int32Ptr(req.ChunkSize),
		MinCharactersPerChunk: protoconv.Int32Ptr(req.MinCharactersPerChunk),
		Recipe:                req.Recipe,
		Lang:                  req.Lang,
		Normalize:             protoconv.ToProtoNormalize(req.Normalize),
	}

	return c.call(ctx, chunkerconnect.TextChunkerServiceChunkRecursiveProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return c.rpc.ChunkRecursive(ctx, connect.NewRequest(msg))
		})
}

// ChunkLate calls TextChunkerService/ChunkLate: late chunking over a
// long-context embedding model.
func (c *Client) ChunkLate(ctx context.Context, req *LateRequest) ([][]common.TextChunk, error) {
	msg := &chunkerpb.ChunkLateRequest{
		Texts:                 req.Texts,
		Model:                 req.Model,
		ChunkSize:             protoconv.Int32Ptr(req.ChunkSize),
		MinCharactersPerChunk: protoconv.Int32Ptr(req.MinCharactersPerChunk),
		Recipe:                req.Recipe,
		Lang:                  req.Lang,
		Normalize:             protoconv.ToProtoNormalize(req.Normalize),
	}

	return c.call(ctx, chunkerconnect.TextChunkerServiceChunkLateProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return c.rpc.ChunkLate(ctx, connect.NewRequest(msg))
		})
}

// ChunkNeural calls TextChunkerService/ChunkNeural: model-predicted chunk
// boundaries.
func (c *Client) ChunkNeural(ctx context.Context, req *NeuralRequest) ([][]common.TextChunk, error) {
	msg := &chunkerpb.ChunkNeuralRequest{
		Texts:                 req.Texts,
		Model:                 req.Model,
		MinCharactersPerChunk: protoconv.Int32Ptr(req.MinCharactersPerChunk),
		Normalize:             protoconv.ToProtoNormalize(req.Normalize),
	}

	return c.call(ctx, chunkerconnect.TextChunkerServiceChunkNeuralProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return c.rpc.ChunkNeural(ctx, connect.NewRequest(msg))
		})
}

// ChunkSlumber calls TextChunkerService/ChunkSlumber: LLM-assisted
// agentic chunking.
func (c *Client) ChunkSlumber(ctx context.Context, req *SlumberRequest) ([][]common.TextChunk, error) {
	tokenizer, err := req.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	genie, err := req.Genie.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkSlumberRequest{
		Texts:                 req.Texts,
		Tokenizer:             tokenizer,
		ChunkSize:             protoconv.Int32Ptr(req.ChunkSize),
		Recipe:                req.Recipe,
		Lang:                  req.Lang,
		CandidateSize:         protoconv.Int32Ptr(req.CandidateSize),
		MinCharactersPerChunk: protoconv.Int32Ptr(req.MinCharactersPerChunk),
		Normalize:             protoconv.ToProtoNormalize(req.Normalize),
		Genie:                 genie,
		Model:                 req.Model,
		BaseUrl:               req.BaseURL,
	}

	return c.call(ctx, chunkerconnect.TextChunkerServiceChunkSlumberProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return c.rpc.ChunkSlumber(ctx, connect.NewRequest(msg))
		})
}

func (c *Client) call(ctx context.Context, procedure string,
	send func(context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error),
) ([][]common.TextChunk, error) {
	resp, err := send(transport.EnsureDeployment(ctx, c.deployment))
	if err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	results := resp.Msg.GetResults()

	out := make([][]common.TextChunk, len(results))
	for i, r := range results {
		out[i] = protoconv.FromProtoTextChunks(r.GetChunks())
	}

	return out, nil
}
