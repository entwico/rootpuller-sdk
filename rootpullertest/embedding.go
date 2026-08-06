package rootpullertest

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/entwico/rootpuller-sdk/embedding"
	embeddingpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/embedding"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/embedding/embeddingconnect"
)

// Embedding is a facade-typed fake VectorEmbeddingService. EmbedFunc
// returns one dense vector per input; a nil EmbedFunc returns [1, 0, ...]
// vectors of dimension 4. Models backs ListModels.
type Embedding struct {
	EmbedFunc func(inputs []embedding.Input) ([][]float32, error)
	Models    []embedding.ModelInfo
}

func (f *Embedding) register(mux *http.ServeMux) {
	mux.Handle(embeddingconnect.NewVectorEmbeddingServiceHandler(&embeddingHandler{fake: f}))
}

type embeddingHandler struct {
	embeddingconnect.UnimplementedVectorEmbeddingServiceHandler

	fake *Embedding
}

func embedResponse(req *embeddingpb.EmbedRequest, vectors [][]float32) *embeddingpb.EmbedResponse {
	resp := &embeddingpb.EmbedResponse{
		Model: req.GetModel(),
		Mode:  req.GetMode(),
		Task:  req.GetTask(),
	}
	for _, vec := range vectors {
		resp.DenseDimension = int32(len(vec)) //nolint:gosec // test-fixture vector lengths fit int32
		resp.Results = append(resp.Results, &embeddingpb.EmbeddingResult{
			Vector: &embeddingpb.EmbeddingResult_Dense{Dense: &embeddingpb.DenseVector{Values: vec}},
		})
	}

	return resp
}

func (h *embeddingHandler) Embed(_ context.Context, req *connect.Request[embeddingpb.EmbedRequest]) (*connect.Response[embeddingpb.EmbedResponse], error) {
	vectors, err := h.vectors(req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(embedResponse(req.Msg, vectors)), nil
}

func (h *embeddingHandler) EmbedStream(_ context.Context, req *connect.Request[embeddingpb.EmbedRequest], stream *connect.ServerStream[embeddingpb.EmbedResponse]) error {
	vectors, err := h.vectors(req.Msg)
	if err != nil {
		return err
	}

	for _, vec := range vectors {
		resp := embedResponse(req.Msg, [][]float32{vec})
		if err := stream.Send(resp); err != nil {
			return err
		}
	}

	return nil
}

func (h *embeddingHandler) ListModels(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[embeddingpb.ListModelsResponse], error) {
	resp := &embeddingpb.ListModelsResponse{}
	for _, m := range h.fake.Models {
		resp.Models = append(resp.Models, &embeddingpb.ModelInfo{
			Model: &embeddingpb.ModelRef{
				ModelId: m.Model.ModelID,
				Backend: embeddingpb.ModelBackend_MODEL_BACKEND_LOCAL,
			},
			DenseDimension: int32(m.DenseDimension), //nolint:gosec // test-fixture dimensions fit int32
			MaxTokens:      int32(m.MaxTokens),      //nolint:gosec // test-fixture limits fit int32
			Loaded:         m.Loaded,
			Description:    m.Description,
		})
	}

	return connect.NewResponse(resp), nil
}

// vectors reassembles the facade inputs and runs the hook.
func (h *embeddingHandler) vectors(req *embeddingpb.EmbedRequest) ([][]float32, error) {
	inputs := make([]embedding.Input, len(req.GetInputs()))
	for i, in := range req.GetInputs() {
		inputs[i] = embedding.Input{Content: in.GetContent(), Metadata: in.GetMetadata()}
	}

	embedFunc := h.fake.EmbedFunc
	if embedFunc == nil {
		embedFunc = func(inputs []embedding.Input) ([][]float32, error) {
			out := make([][]float32, len(inputs))
			for i := range inputs {
				out[i] = []float32{1, 0, 0, 0}
			}

			return out, nil
		}
	}

	return embedFunc(inputs)
}
