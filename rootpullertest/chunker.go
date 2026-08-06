package rootpullertest

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/common"
	chunkerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker/chunkerconnect"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
)

// Chunker is a facade-typed fake TextChunkerService. ChunkFunc receives
// the RPC method name (e.g. "ChunkToken") and the input texts, and returns
// one chunk slice per text. A nil ChunkFunc echoes each text as a single
// chunk.
type Chunker struct {
	ChunkFunc func(method string, texts []string) ([][]common.TextChunk, error)
}

func (f *Chunker) register(mux *http.ServeMux) {
	mux.Handle(chunkerconnect.NewTextChunkerServiceHandler(&chunkerHandler{fake: f}))
}

type chunkerHandler struct {
	chunkerconnect.UnimplementedTextChunkerServiceHandler

	fake *Chunker
}

func (h *chunkerHandler) respond(method string, texts []string) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	chunkFunc := h.fake.ChunkFunc
	if chunkFunc == nil {
		chunkFunc = func(_ string, texts []string) ([][]common.TextChunk, error) {
			out := make([][]common.TextChunk, len(texts))
			for i, t := range texts {
				out[i] = []common.TextChunk{{Text: t, EndIndex: len(t), TokenCount: len(t)}}
			}
			return out, nil
		}
	}
	rows, err := chunkFunc(method, texts)
	if err != nil {
		return nil, err
	}
	resp := &chunkerpb.TextChunkResponse{}
	for _, row := range rows {
		result := &chunkerpb.TextChunkResponse_Result{}
		for _, c := range row {
			result.Chunks = append(result.Chunks, &commonpb.TextChunk{
				Text:       c.Text,
				StartIndex: int32(c.StartIndex),
				EndIndex:   int32(c.EndIndex),
				TokenCount: int32(c.TokenCount),
			})
		}
		resp.Results = append(resp.Results, result)
	}
	return connect.NewResponse(resp), nil
}

func (h *chunkerHandler) ChunkSemantic(_ context.Context, req *connect.Request[chunkerpb.ChunkSemanticRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	return h.respond("ChunkSemantic", req.Msg.GetTexts())
}

func (h *chunkerHandler) ChunkToken(_ context.Context, req *connect.Request[chunkerpb.ChunkTokenRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	return h.respond("ChunkToken", req.Msg.GetTexts())
}

func (h *chunkerHandler) ChunkSentence(_ context.Context, req *connect.Request[chunkerpb.ChunkSentenceRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	return h.respond("ChunkSentence", req.Msg.GetTexts())
}

func (h *chunkerHandler) ChunkCode(_ context.Context, req *connect.Request[chunkerpb.ChunkCodeRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	return h.respond("ChunkCode", req.Msg.GetTexts())
}

func (h *chunkerHandler) ChunkRecursive(_ context.Context, req *connect.Request[chunkerpb.ChunkRecursiveRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	return h.respond("ChunkRecursive", req.Msg.GetTexts())
}

func (h *chunkerHandler) ChunkLate(_ context.Context, req *connect.Request[chunkerpb.ChunkLateRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	return h.respond("ChunkLate", req.Msg.GetTexts())
}

func (h *chunkerHandler) ChunkNeural(_ context.Context, req *connect.Request[chunkerpb.ChunkNeuralRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	return h.respond("ChunkNeural", req.Msg.GetTexts())
}

func (h *chunkerHandler) ChunkSlumber(_ context.Context, req *connect.Request[chunkerpb.ChunkSlumberRequest]) (*connect.Response[chunkerpb.TextChunkResponse], error) {
	return h.respond("ChunkSlumber", req.Msg.GetTexts())
}
