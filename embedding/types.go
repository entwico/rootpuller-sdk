package embedding

import (
	"fmt"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/common"
	embeddingpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/embedding"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// Backend selects where the embedding model runs. The zero value keeps
// the server default.
type Backend string

const (
	BackendDefault Backend = ""
	BackendLocal   Backend = "local"
	BackendGoogle  Backend = "google"
	BackendOpenAI  Backend = "openai"
	BackendCohere  Backend = "cohere"
)

var backendToProto = map[Backend]embeddingpb.ModelBackend{
	BackendDefault: embeddingpb.ModelBackend_MODEL_BACKEND_UNSPECIFIED,
	BackendLocal:   embeddingpb.ModelBackend_MODEL_BACKEND_LOCAL,
	BackendGoogle:  embeddingpb.ModelBackend_MODEL_BACKEND_GOOGLE,
	BackendOpenAI:  embeddingpb.ModelBackend_MODEL_BACKEND_OPENAI,
	BackendCohere:  embeddingpb.ModelBackend_MODEL_BACKEND_COHERE,
}

var backendFromProto = map[embeddingpb.ModelBackend]Backend{
	embeddingpb.ModelBackend_MODEL_BACKEND_UNSPECIFIED: BackendDefault,
	embeddingpb.ModelBackend_MODEL_BACKEND_LOCAL:       BackendLocal,
	embeddingpb.ModelBackend_MODEL_BACKEND_GOOGLE:      BackendGoogle,
	embeddingpb.ModelBackend_MODEL_BACKEND_OPENAI:      BackendOpenAI,
	embeddingpb.ModelBackend_MODEL_BACKEND_COHERE:      BackendCohere,
}

// Task tunes the embedding for its downstream use. The zero value keeps
// the server default.
type Task string

const (
	TaskDefault            Task = ""
	TaskRetrievalDocument  Task = "retrieval-document"
	TaskRetrievalQuery     Task = "retrieval-query"
	TaskClustering         Task = "clustering"
	TaskClassification     Task = "classification"
	TaskReranking          Task = "reranking"
	TaskQuestionAnswering  Task = "question-answering"
	TaskFactVerification   Task = "fact-verification"
	TaskSemanticSimilarity Task = "semantic-similarity"
	TaskCodeRetrieval      Task = "code-retrieval"
)

var taskToProto = map[Task]embeddingpb.EmbeddingTask{
	TaskDefault:            embeddingpb.EmbeddingTask_EMBEDDING_TASK_UNSPECIFIED,
	TaskRetrievalDocument:  embeddingpb.EmbeddingTask_EMBEDDING_TASK_RETRIEVAL_DOCUMENT,
	TaskRetrievalQuery:     embeddingpb.EmbeddingTask_EMBEDDING_TASK_RETRIEVAL_QUERY,
	TaskClustering:         embeddingpb.EmbeddingTask_EMBEDDING_TASK_CLUSTERING,
	TaskClassification:     embeddingpb.EmbeddingTask_EMBEDDING_TASK_CLASSIFICATION,
	TaskReranking:          embeddingpb.EmbeddingTask_EMBEDDING_TASK_RERANKING,
	TaskQuestionAnswering:  embeddingpb.EmbeddingTask_EMBEDDING_TASK_QUESTION_ANSWERING,
	TaskFactVerification:   embeddingpb.EmbeddingTask_EMBEDDING_TASK_FACT_VERIFICATION,
	TaskSemanticSimilarity: embeddingpb.EmbeddingTask_EMBEDDING_TASK_SEMANTIC_SIMILARITY,
	TaskCodeRetrieval:      embeddingpb.EmbeddingTask_EMBEDDING_TASK_CODE_RETRIEVAL,
}

var taskFromProto = invertMap(taskToProto)

// Mode selects the embedding representation. Sparse, multi-vector, and
// hybrid modes are available on the local backend only; cloud backends
// return ErrUnimplemented for them.
type Mode string

const (
	ModeDefault     Mode = ""
	ModeDense       Mode = "dense"
	ModeSparse      Mode = "sparse"
	ModeMultiVector Mode = "multi-vector"
	ModeHybrid      Mode = "hybrid"
)

var modeToProto = map[Mode]embeddingpb.EmbeddingMode{
	ModeDefault:     embeddingpb.EmbeddingMode_EMBEDDING_MODE_UNSPECIFIED,
	ModeDense:       embeddingpb.EmbeddingMode_EMBEDDING_MODE_DENSE,
	ModeSparse:      embeddingpb.EmbeddingMode_EMBEDDING_MODE_SPARSE,
	ModeMultiVector: embeddingpb.EmbeddingMode_EMBEDDING_MODE_MULTI_VECTOR,
	ModeHybrid:      embeddingpb.EmbeddingMode_EMBEDDING_MODE_HYBRID,
}

var modeFromProto = invertMap(modeToProto)

// InferenceBackend selects the local inference engine. The zero value
// keeps the server default.
type InferenceBackend string

const (
	InferenceBackendDefault              InferenceBackend = ""
	InferenceBackendFastEmbed            InferenceBackend = "fastembed"
	InferenceBackendSentenceTransformers InferenceBackend = "sentence-transformers"
	InferenceBackendFlagEmbedding        InferenceBackend = "flag-embedding"
	InferenceBackendModel2Vec            InferenceBackend = "model2vec"
)

var inferenceBackendToProto = map[InferenceBackend]embeddingpb.InferenceBackend{
	InferenceBackendDefault:              embeddingpb.InferenceBackend_INFERENCE_BACKEND_UNSPECIFIED,
	InferenceBackendFastEmbed:            embeddingpb.InferenceBackend_INFERENCE_BACKEND_FASTEMBED,
	InferenceBackendSentenceTransformers: embeddingpb.InferenceBackend_INFERENCE_BACKEND_SENTENCE_TRANSFORMERS,
	InferenceBackendFlagEmbedding:        embeddingpb.InferenceBackend_INFERENCE_BACKEND_FLAG_EMBEDDING,
	InferenceBackendModel2Vec:            embeddingpb.InferenceBackend_INFERENCE_BACKEND_MODEL2VEC,
}

var inferenceBackendFromProto = invertMap(inferenceBackendToProto)

func invertMap[K, V comparable](m map[K]V) map[V]K {
	out := make(map[V]K, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}

func invalidArgument(msg string) error {
	return apierror.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// ModelRef names an embedding model on a backend.
type ModelRef struct {
	Backend Backend
	ModelID string
	// InferenceBackend applies to local models only.
	InferenceBackend InferenceBackend
}

func (m ModelRef) toProto() (*embeddingpb.ModelRef, error) {
	backend, ok := backendToProto[m.Backend]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown embedding backend %q", m.Backend))
	}
	inference, ok := inferenceBackendToProto[m.InferenceBackend]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown inference backend %q", m.InferenceBackend))
	}
	return &embeddingpb.ModelRef{Backend: backend, ModelId: m.ModelID, InferenceBackend: inference}, nil
}

func modelRefFromProto(m *embeddingpb.ModelRef) ModelRef {
	if m == nil {
		return ModelRef{}
	}
	return ModelRef{
		Backend:          enumFromProto(backendFromProto, m.GetBackend()),
		ModelID:          m.GetModelId(),
		InferenceBackend: enumFromProto(inferenceBackendFromProto, m.GetInferenceBackend()),
	}
}

// enumFromProto tolerates enum values newer than this SDK by falling back
// to the proto identifier string.
func enumFromProto[P interface {
	comparable
	fmt.Stringer
}, F ~string](m map[P]F, v P) F {
	if f, ok := m[v]; ok {
		return f
	}
	return F(v.String())
}

// Input is one text to embed. The optional metadata map supports the
// reserved "title" key for models that embed titles separately.
type Input struct {
	Content  string
	Metadata map[string]string
}

// Text wraps plain strings as Inputs.
func Text(texts ...string) []Input {
	inputs := make([]Input, len(texts))
	for i, t := range texts {
		inputs[i] = Input{Content: t}
	}
	return inputs
}

// Request configures Embed and EmbedStream.
type Request struct {
	Inputs []Input
	Model  ModelRef
	Mode   Mode
	Task   Task
	// Dimensions truncates MRL-capable dense embeddings.
	Dimensions *int
	// MaxTokens overrides the model's input truncation limit.
	MaxTokens *int
	// Normalize L2-normalizes dense vectors server-side.
	Normalize bool
}

func (r *Request) toProto() (*embeddingpb.EmbedRequest, error) {
	model, err := r.Model.toProto()
	if err != nil {
		return nil, err
	}
	mode, ok := modeToProto[r.Mode]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown embedding mode %q", r.Mode))
	}
	task, ok := taskToProto[r.Task]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown embedding task %q", r.Task))
	}
	inputs := make([]*embeddingpb.TextInput, len(r.Inputs))
	for i, in := range r.Inputs {
		inputs[i] = &embeddingpb.TextInput{Content: in.Content, Metadata: in.Metadata}
	}
	msg := &embeddingpb.EmbedRequest{Inputs: inputs, Model: model, Mode: mode, Task: task}
	if r.Dimensions != nil || r.MaxTokens != nil || r.Normalize {
		msg.Options = &embeddingpb.EmbeddingOptions{
			Dimensions: protoconv.Int32Ptr(r.Dimensions),
			MaxTokens:  protoconv.Int32Ptr(r.MaxTokens),
			Normalize:  r.Normalize,
		}
	}
	return msg, nil
}

// SparseVector is a sparse embedding as parallel index/value slices.
type SparseVector struct {
	Indices []uint32
	Values  []float32
}

// Embedding is one embedding result. Kind tells which representation the
// server returned: Dense is set for ModeDense, Sparse for ModeSparse,
// Multi for ModeMultiVector, and both Dense and Sparse for ModeHybrid.
type Embedding struct {
	Kind   Mode
	Dense  []float32
	Sparse *SparseVector
	Multi  [][]float32
}

func embeddingFromProto(r *embeddingpb.EmbeddingResult) Embedding {
	switch v := r.GetVector().(type) {
	case *embeddingpb.EmbeddingResult_Dense:
		return Embedding{Kind: ModeDense, Dense: v.Dense.GetValues()}
	case *embeddingpb.EmbeddingResult_Sparse:
		return Embedding{Kind: ModeSparse, Sparse: sparseFromProto(v.Sparse)}
	case *embeddingpb.EmbeddingResult_MultiVector:
		tokens := v.MultiVector.GetTokenVectors()
		multi := make([][]float32, len(tokens))
		for i, tv := range tokens {
			multi[i] = tv.GetValues()
		}
		return Embedding{Kind: ModeMultiVector, Multi: multi}
	case *embeddingpb.EmbeddingResult_Hybrid:
		return Embedding{
			Kind:   ModeHybrid,
			Dense:  v.Hybrid.GetDense().GetValues(),
			Sparse: sparseFromProto(v.Hybrid.GetSparse()),
		}
	default:
		return Embedding{}
	}
}

func sparseFromProto(s *embeddingpb.SparseVector) *SparseVector {
	if s == nil {
		return nil
	}
	return &SparseVector{Indices: s.GetIndices(), Values: s.GetValues()}
}

// Response is the result of Embed: one Embedding per input, in order.
type Response struct {
	Embeddings     []Embedding
	Model          ModelRef
	Mode           Mode
	Task           Task
	DenseDimension int
	Usage          common.Usage
}

// StreamResult is one EmbedStream frame: the embedding of a single input,
// in request order, with the response metadata repeated per frame.
type StreamResult struct {
	Embedding      Embedding
	Model          ModelRef
	Mode           Mode
	Task           Task
	DenseDimension int
	Usage          common.Usage
}

// ModelInfo describes one model reported by ListModels.
type ModelInfo struct {
	Model             ModelRef
	SupportedModes    []Mode
	DenseDimension    int
	MRLTruncationDims []int
	MaxTokens         int
	SupportedTasks    []Task
	Loaded            bool
	Description       string
}

func modelInfoFromProto(m *embeddingpb.ModelInfo) ModelInfo {
	modes := make([]Mode, len(m.GetSupportedModes()))
	for i, v := range m.GetSupportedModes() {
		modes[i] = enumFromProto(modeFromProto, v)
	}
	tasks := make([]Task, len(m.GetSupportedTasks()))
	for i, v := range m.GetSupportedTasks() {
		tasks[i] = enumFromProto(taskFromProto, v)
	}
	dims := make([]int, len(m.GetMrlTruncationDims()))
	for i, v := range m.GetMrlTruncationDims() {
		dims[i] = int(v)
	}
	return ModelInfo{
		Model:             modelRefFromProto(m.GetModel()),
		SupportedModes:    modes,
		DenseDimension:    int(m.GetDenseDimension()),
		MRLTruncationDims: dims,
		MaxTokens:         int(m.GetMaxTokens()),
		SupportedTasks:    tasks,
		Loaded:            m.GetLoaded(),
		Description:       m.GetDescription(),
	}
}
