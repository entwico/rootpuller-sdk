package rootpullertest

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rerankpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank/rerankconnect"
	"github.com/entwico/rootpuller-sdk/rerank"
)

// Rerank is a facade-typed fake RerankService. RerankFunc receives the
// query and documents and returns the scored results; a nil RerankFunc
// echoes the documents in order with descending scores. ListModels serves
// Models; when nil it serves one canned loaded model.
type Rerank struct {
	RerankFunc func(query string, documents []string) ([]rerank.Result, error)
	Models     []rerank.ModelInfo
}

func (f *Rerank) register(mux *http.ServeMux) {
	mux.Handle(rerankconnect.NewRerankServiceHandler(&rerankHandler{fake: f}))
}

type rerankHandler struct {
	rerankconnect.UnimplementedRerankServiceHandler

	fake *Rerank
}

func (h *rerankHandler) Rerank(_ context.Context, req *connect.Request[rerankpb.RerankRequest]) (*connect.Response[rerankpb.RerankResponse], error) {
	rerankFunc := h.fake.RerankFunc
	if rerankFunc == nil {
		rerankFunc = func(_ string, documents []string) ([]rerank.Result, error) {
			out := make([]rerank.Result, len(documents))
			for i, d := range documents {
				out[i] = rerank.Result{
					Index:          i,
					RelevanceScore: 1 / float32(i+1),
					Document:       d,
				}
			}

			return out, nil
		}
	}

	results, err := rerankFunc(req.Msg.GetQuery(), req.Msg.GetDocuments())
	if err != nil {
		return nil, err
	}

	resp := &rerankpb.RerankResponse{Model: req.Msg.GetModel()}
	for _, r := range results {
		resp.Results = append(resp.Results, &rerankpb.RerankResult{
			Index:          int32(r.Index), //nolint:gosec // test-fixture indexes fit int32
			RelevanceScore: r.RelevanceScore,
			Document:       r.Document,
		})
	}

	return connect.NewResponse(resp), nil
}

func (h *rerankHandler) ListModels(_ context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[rerankpb.ListRerankModelsResponse], error) {
	models := h.fake.Models
	if models == nil {
		models = []rerank.ModelInfo{{
			Model:       rerank.ModelRef{ModelID: "BAAI/bge-reranker-v2-m3"},
			MaxTokens:   8192,
			Loaded:      true,
			Description: "BGE Reranker v2-m3 (multilingual)",
		}}
	}

	resp := &rerankpb.ListRerankModelsResponse{}
	for _, m := range models {
		resp.Models = append(resp.Models, &rerankpb.RerankModelInfo{
			Model: &rerankpb.RerankModelRef{
				ModelId:          m.Model.ModelID,
				InferenceBackend: rerankBackendToProto(m.Model.InferenceBackend),
			},
			MaxTokens:   int32(m.MaxTokens), //nolint:gosec // test-fixture limits fit int32
			Loaded:      m.Loaded,
			Description: m.Description,
		})
	}

	return connect.NewResponse(resp), nil
}

func rerankBackendToProto(b rerank.InferenceBackend) rerankpb.RerankInferenceBackend {
	switch b {
	case rerank.InferenceBackendFlagReranker:
		return rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_FLAG_RERANKER
	case rerank.InferenceBackendSentenceTransformers:
		return rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_SENTENCE_TRANSFORMERS
	case rerank.InferenceBackendDefault:
		return rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_UNSPECIFIED
	}

	return rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_UNSPECIFIED
}
