// Package chunker wraps com.entwico.rootpuller.chunker.TextChunkerService:
// semantic, token, sentence, code, recursive, late, neural, and slumber
// text chunking (Chonkie). All methods return one []rootpullersdk.TextChunk per
// input text, row-aligned with the texts argument.
package chunker

import (
	"context"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	chunkerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker/chunkerconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the TextChunkerService.
type Service struct {
	rpc        chunkerconnect.TextChunkerServiceClient
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

// NewService builds a TextChunkerService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: chunkerconnect.NewTextChunkerServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// SemanticOptions tunes a ChunkSemantic call. Nil keeps all defaults.
type SemanticOptions struct {
	// Model is the embedding model used for similarity; "" keeps the
	// server default (minishlab/potion-base-32M).
	Model string
	// Threshold is the similarity threshold 0.0-1.0 (nil = server
	// default 0.8).
	Threshold *float32
	// ChunkSize is the max tokens per chunk (0 = server default 512).
	ChunkSize int
	// SimilarityWindow is the window for similarity comparison (0 =
	// server default 1).
	SimilarityWindow int
	// MinSentencesPerChunk keeps the server default 1 when 0.
	MinSentencesPerChunk int
	// MinCharactersPerSentence keeps the server default 12 when 0.
	MinCharactersPerSentence int
	// Delimiters keeps the server default [". ", "! ", "? ", "\n"] when
	// empty.
	Delimiters         []string
	DelimiterInclusion DelimiterInclusion
	// SkipWindow keeps the server default 0 when 0.
	SkipWindow int
	// FilterWindow keeps the server default 5 when 0.
	FilterWindow int
	// FilterPolyorder keeps the server default 3 when 0.
	FilterPolyorder int
	// FilterTolerance keeps the server default 0.2 when nil.
	FilterTolerance *float32
	Normalize       *rootpullersdk.NormalizeOptions
	// MinCharactersPerChunk merges shorter chunks into a neighbour (0 =
	// off).
	MinCharactersPerChunk int
}

// ChunkSemantic calls TextChunkerService/ChunkSemantic: embedding-driven
// splitting at semantic similarity boundaries.
func (s *Service) ChunkSemantic(ctx context.Context, texts []string, opts *SemanticOptions) ([][]rootpullersdk.TextChunk, error) {
	if opts == nil {
		opts = &SemanticOptions{}
	}

	inclusion, err := opts.DelimiterInclusion.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkSemanticRequest{
		Texts:                    texts,
		Model:                    opts.Model,
		Threshold:                opts.Threshold,
		ChunkSize:                positiveInt32(opts.ChunkSize),
		SimilarityWindow:         positiveInt32(opts.SimilarityWindow),
		MinSentencesPerChunk:     positiveInt32(opts.MinSentencesPerChunk),
		MinCharactersPerSentence: positiveInt32(opts.MinCharactersPerSentence),
		Delimiters:               opts.Delimiters,
		DelimiterInclusion:       inclusion,
		SkipWindow:               positiveInt32(opts.SkipWindow),
		FilterWindow:             positiveInt32(opts.FilterWindow),
		FilterPolyorder:          positiveInt32(opts.FilterPolyorder),
		FilterTolerance:          opts.FilterTolerance,
		Normalize:                protoconv.ToProtoNormalize(opts.Normalize),
		MinCharactersPerChunk:    positiveInt32(opts.MinCharactersPerChunk),
	}

	return s.call(ctx, chunkerconnect.TextChunkerServiceChunkSemanticProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return s.rpc.ChunkSemantic(ctx, connect.NewRequest(msg))
		})
}

// TokenOptions tunes a ChunkToken call. Nil keeps all defaults.
type TokenOptions struct {
	Tokenizer Tokenizer
	// ChunkSize is the max tokens per chunk (0 = server default 512).
	ChunkSize int
	// ChunkOverlap is the token overlap between chunks (0 = server
	// default 0).
	ChunkOverlap int
	Normalize    *rootpullersdk.NormalizeOptions
}

// ChunkToken calls TextChunkerService/ChunkToken: fixed-size token
// windows with optional overlap.
func (s *Service) ChunkToken(ctx context.Context, texts []string, opts *TokenOptions) ([][]rootpullersdk.TextChunk, error) {
	if opts == nil {
		opts = &TokenOptions{}
	}

	tokenizer, err := opts.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkTokenRequest{
		Texts:        texts,
		Tokenizer:    tokenizer,
		ChunkSize:    positiveInt32(opts.ChunkSize),
		ChunkOverlap: positiveInt32(opts.ChunkOverlap),
		Normalize:    protoconv.ToProtoNormalize(opts.Normalize),
	}

	return s.call(ctx, chunkerconnect.TextChunkerServiceChunkTokenProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return s.rpc.ChunkToken(ctx, connect.NewRequest(msg))
		})
}

// SentenceOptions tunes a ChunkSentence call. Nil keeps all defaults.
type SentenceOptions struct {
	Tokenizer                Tokenizer
	ChunkSize                int
	ChunkOverlap             int
	MinSentencesPerChunk     int
	MinCharactersPerSentence int
	// Approximate toggles approximate token counting; nil keeps the
	// server default (true).
	Approximate        *bool
	Delimiters         []string
	DelimiterInclusion DelimiterInclusion
	Normalize          *rootpullersdk.NormalizeOptions
}

// ChunkSentence calls TextChunkerService/ChunkSentence: sentence-boundary
// chunking with token budgets.
func (s *Service) ChunkSentence(ctx context.Context, texts []string, opts *SentenceOptions) ([][]rootpullersdk.TextChunk, error) {
	if opts == nil {
		opts = &SentenceOptions{}
	}

	tokenizer, err := opts.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	inclusion, err := opts.DelimiterInclusion.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkSentenceRequest{
		Texts:                    texts,
		Tokenizer:                tokenizer,
		ChunkSize:                positiveInt32(opts.ChunkSize),
		ChunkOverlap:             positiveInt32(opts.ChunkOverlap),
		MinSentencesPerChunk:     positiveInt32(opts.MinSentencesPerChunk),
		MinCharactersPerSentence: positiveInt32(opts.MinCharactersPerSentence),
		Approximate:              opts.Approximate,
		Delimiters:               opts.Delimiters,
		DelimiterInclusion:       inclusion,
		Normalize:                protoconv.ToProtoNormalize(opts.Normalize),
	}

	return s.call(ctx, chunkerconnect.TextChunkerServiceChunkSentenceProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return s.rpc.ChunkSentence(ctx, connect.NewRequest(msg))
		})
}

// CodeOptions tunes a ChunkCode call. Nil keeps all defaults.
type CodeOptions struct {
	Tokenizer Tokenizer
	ChunkSize int
	// Language is the programming language ("" lets the server infer it).
	Language string
}

// ChunkCode calls TextChunkerService/ChunkCode: syntax-aware chunking of
// source code.
func (s *Service) ChunkCode(ctx context.Context, texts []string, opts *CodeOptions) ([][]rootpullersdk.TextChunk, error) {
	if opts == nil {
		opts = &CodeOptions{}
	}

	tokenizer, err := opts.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkCodeRequest{
		Texts:     texts,
		Tokenizer: tokenizer,
		ChunkSize: positiveInt32(opts.ChunkSize),
		Language:  nonEmptyString(opts.Language),
	}

	return s.call(ctx, chunkerconnect.TextChunkerServiceChunkCodeProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return s.rpc.ChunkCode(ctx, connect.NewRequest(msg))
		})
}

// RecursiveOptions tunes a ChunkRecursive call. Nil keeps all defaults.
type RecursiveOptions struct {
	Tokenizer             Tokenizer
	ChunkSize             int
	MinCharactersPerChunk int
	// Recipe keeps the server default "default" when "".
	Recipe string
	// Lang keeps the server default "en" when "".
	Lang      string
	Normalize *rootpullersdk.NormalizeOptions
}

// ChunkRecursive calls TextChunkerService/ChunkRecursive: hierarchical
// splitting by recipe-defined separators.
func (s *Service) ChunkRecursive(ctx context.Context, texts []string, opts *RecursiveOptions) ([][]rootpullersdk.TextChunk, error) {
	if opts == nil {
		opts = &RecursiveOptions{}
	}

	tokenizer, err := opts.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkRecursiveRequest{
		Texts:                 texts,
		Tokenizer:             tokenizer,
		ChunkSize:             positiveInt32(opts.ChunkSize),
		MinCharactersPerChunk: positiveInt32(opts.MinCharactersPerChunk),
		Recipe:                nonEmptyString(opts.Recipe),
		Lang:                  nonEmptyString(opts.Lang),
		Normalize:             protoconv.ToProtoNormalize(opts.Normalize),
	}

	return s.call(ctx, chunkerconnect.TextChunkerServiceChunkRecursiveProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return s.rpc.ChunkRecursive(ctx, connect.NewRequest(msg))
		})
}

// LateOptions tunes a ChunkLate call. Nil keeps all defaults.
type LateOptions struct {
	// Model is the embedding model; "" keeps the server default.
	Model                 string
	ChunkSize             int
	MinCharactersPerChunk int
	Recipe                string
	Lang                  string
	Normalize             *rootpullersdk.NormalizeOptions
}

// ChunkLate calls TextChunkerService/ChunkLate: late chunking over a
// long-context embedding model.
func (s *Service) ChunkLate(ctx context.Context, texts []string, opts *LateOptions) ([][]rootpullersdk.TextChunk, error) {
	if opts == nil {
		opts = &LateOptions{}
	}

	msg := &chunkerpb.ChunkLateRequest{
		Texts:                 texts,
		Model:                 opts.Model,
		ChunkSize:             positiveInt32(opts.ChunkSize),
		MinCharactersPerChunk: positiveInt32(opts.MinCharactersPerChunk),
		Recipe:                nonEmptyString(opts.Recipe),
		Lang:                  nonEmptyString(opts.Lang),
		Normalize:             protoconv.ToProtoNormalize(opts.Normalize),
	}

	return s.call(ctx, chunkerconnect.TextChunkerServiceChunkLateProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return s.rpc.ChunkLate(ctx, connect.NewRequest(msg))
		})
}

// NeuralOptions tunes a ChunkNeural call. Nil keeps all defaults.
type NeuralOptions struct {
	// Model is the neural chunking model; "" keeps the server default.
	Model                 string
	MinCharactersPerChunk int
	Normalize             *rootpullersdk.NormalizeOptions
}

// ChunkNeural calls TextChunkerService/ChunkNeural: model-predicted chunk
// boundaries.
func (s *Service) ChunkNeural(ctx context.Context, texts []string, opts *NeuralOptions) ([][]rootpullersdk.TextChunk, error) {
	if opts == nil {
		opts = &NeuralOptions{}
	}

	msg := &chunkerpb.ChunkNeuralRequest{
		Texts:                 texts,
		Model:                 opts.Model,
		MinCharactersPerChunk: positiveInt32(opts.MinCharactersPerChunk),
		Normalize:             protoconv.ToProtoNormalize(opts.Normalize),
	}

	return s.call(ctx, chunkerconnect.TextChunkerServiceChunkNeuralProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return s.rpc.ChunkNeural(ctx, connect.NewRequest(msg))
		})
}

// SlumberOptions tunes a ChunkSlumber call. Nil keeps all defaults.
type SlumberOptions struct {
	Tokenizer Tokenizer
	// ChunkSize keeps the server default 1024 when 0.
	ChunkSize int
	Recipe    string
	Lang      string
	// CandidateSize keeps the server default 128 when 0.
	CandidateSize int
	// MinCharactersPerChunk keeps the server default 24 when 0.
	MinCharactersPerChunk int
	Normalize             *rootpullersdk.NormalizeOptions
	Genie                 Genie
	// Model overrides the LLM model; "" keeps the provider default.
	Model string
	// BaseURL overrides the LLM endpoint; "" keeps the provider default.
	BaseURL string
}

// ChunkSlumber calls TextChunkerService/ChunkSlumber: LLM-assisted
// agentic chunking.
func (s *Service) ChunkSlumber(ctx context.Context, texts []string, opts *SlumberOptions) ([][]rootpullersdk.TextChunk, error) {
	if opts == nil {
		opts = &SlumberOptions{}
	}

	tokenizer, err := opts.Tokenizer.toProto()
	if err != nil {
		return nil, err
	}

	genie, err := opts.Genie.toProto()
	if err != nil {
		return nil, err
	}

	msg := &chunkerpb.ChunkSlumberRequest{
		Texts:                 texts,
		Tokenizer:             tokenizer,
		ChunkSize:             positiveInt32(opts.ChunkSize),
		Recipe:                nonEmptyString(opts.Recipe),
		Lang:                  nonEmptyString(opts.Lang),
		CandidateSize:         positiveInt32(opts.CandidateSize),
		MinCharactersPerChunk: positiveInt32(opts.MinCharactersPerChunk),
		Normalize:             protoconv.ToProtoNormalize(opts.Normalize),
		Genie:                 genie,
		Model:                 nonEmptyString(opts.Model),
		BaseUrl:               nonEmptyString(opts.BaseURL),
	}

	return s.call(ctx, chunkerconnect.TextChunkerServiceChunkSlumberProcedure,
		func(ctx context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error) {
			return s.rpc.ChunkSlumber(ctx, connect.NewRequest(msg))
		})
}

func (s *Service) call(ctx context.Context, procedure string,
	send func(context.Context) (*connect.Response[chunkerpb.TextChunkResponse], error),
) ([][]rootpullersdk.TextChunk, error) {
	resp, err := send(transport.EnsureDeployment(ctx, s.deployment))
	if err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	results := resp.Msg.GetResults()

	out := make([][]rootpullersdk.TextChunk, len(results))
	for i, r := range results {
		out[i] = protoconv.FromProtoTextChunks(r.GetChunks())
	}

	return out, nil
}

// positiveInt32 maps a plain-value count to the proto optional form: zero
// (or negative) means "server default" and stays unset.
func positiveInt32(v int) *int32 {
	if v <= 0 {
		return nil
	}

	return protoconv.Int32Ptr(&v)
}

// nonEmptyString maps a plain-value string to the proto optional form:
// empty means "server default" and stays unset.
func nonEmptyString(v string) *string {
	if v == "" {
		return nil
	}

	return &v
}
