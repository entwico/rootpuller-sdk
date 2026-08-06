package rerank

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/internal/apierr"
	rerankpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank"
)

// InferenceBackend optionally pins which local Python inference backend
// serves the cross-encoder on the server. The zero value lets the server
// pick automatically based on the model ID.
type InferenceBackend string

const (
	InferenceBackendDefault              InferenceBackend = ""
	InferenceBackendFlagReranker         InferenceBackend = "flag-reranker"         // FlagEmbedding FlagReranker (bge-reranker-v2-m3)
	InferenceBackendSentenceTransformers InferenceBackend = "sentence-transformers" // sentence-transformers CrossEncoder
)

var inferenceBackendToProto = map[InferenceBackend]rerankpb.RerankInferenceBackend{
	InferenceBackendDefault:              rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_UNSPECIFIED,
	InferenceBackendFlagReranker:         rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_FLAG_RERANKER,
	InferenceBackendSentenceTransformers: rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_SENTENCE_TRANSFORMERS,
}

func (b InferenceBackend) toProto() (rerankpb.RerankInferenceBackend, error) {
	v, ok := inferenceBackendToProto[b]
	if !ok {
		return 0, invalidArgument(fmt.Sprintf("unknown inference backend %q", b))
	}

	return v, nil
}

func inferenceBackendFromProto(v rerankpb.RerankInferenceBackend) InferenceBackend {
	switch v {
	case rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_UNSPECIFIED:
		return InferenceBackendDefault
	case rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_FLAG_RERANKER:
		return InferenceBackendFlagReranker
	case rerankpb.RerankInferenceBackend_RERANK_INFERENCE_BACKEND_SENTENCE_TRANSFORMERS:
		return InferenceBackendSentenceTransformers
	default:
		// Unknown value from a newer server: preserve it in the same style
		// as the constants above.
		name := strings.TrimPrefix(v.String(), "RERANK_INFERENCE_BACKEND_")

		return InferenceBackend(strings.ReplaceAll(strings.ToLower(name), "_", "-"))
	}
}

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// ModelRef identifies a locally-served reranker model, e.g.
// {ModelID: "BAAI/bge-reranker-v2-m3"}. The server rejects an empty
// ModelID with InvalidArgument and an unknown model with NotFound.
type ModelRef struct {
	ModelID          string
	InferenceBackend InferenceBackend
}

func (m ModelRef) toProto() (*rerankpb.RerankModelRef, error) {
	backend, err := m.InferenceBackend.toProto()
	if err != nil {
		return nil, err
	}

	return &rerankpb.RerankModelRef{ModelId: m.ModelID, InferenceBackend: backend}, nil
}

func modelRefFromProto(m *rerankpb.RerankModelRef) ModelRef {
	if m == nil {
		return ModelRef{}
	}

	return ModelRef{
		ModelID:          m.GetModelId(),
		InferenceBackend: inferenceBackendFromProto(m.GetInferenceBackend()),
	}
}

// Result is one scored document. RelevanceScore is sigmoid-normalised to
// (0, 1); higher means more relevant. Document is populated only when
// Request.ReturnDocuments is true.
type Result struct {
	Index          int
	RelevanceScore float32
	Document       string
}

// Response is the outcome of Rerank: results sorted by descending
// relevance, plus the model that produced them.
type Response struct {
	Results []Result
	Model   ModelRef
}

// ModelInfo describes one reranker model available on the server. Loaded
// reflects only whether the model's cache directory is present on disk,
// not whether it is loaded into memory.
type ModelInfo struct {
	Model ModelRef
	// MaxTokens is the maximum input context length in tokens for a
	// (query, document) pair; zero if the server's probe failed.
	MaxTokens   int
	Loaded      bool
	Description string
}
