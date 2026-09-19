// Package rerank wraps com.entwico.rootpuller.rerank.RerankService:
// cross-encoder relevance scoring of candidate documents against a query
// (second-stage retrieval), plus discovery of the locally-served reranker
// models.
package rerank

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	rerankpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/rerank/rerankconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the RerankService.
type Service struct {
	rpc          rerankconnect.RerankServiceClient
	deployment   string
	defaultModel string
	backpressure *rootpullersdk.Backpressure
}

// Option configures a Service at construction.
type Option func(*Service)

// WithBackpressure routes every call through the shared admission gate.
// The server pools capacity per deployment across the rootpuller-backed
// services, so pass the SAME *rootpullersdk.Backpressure to every
// routable service client targeting one deployment.
func WithBackpressure(bp *rootpullersdk.Backpressure) Option {
	return func(s *Service) { s.backpressure = bp }
}

// WithDeployment sends the rootpuller-deployment routing header (e.g.
// "local", "cloudrun") on every call. A per-call
// rootpullersdk.ContextWithDeployment value still wins.
func WithDeployment(name string) Option {
	return func(s *Service) { s.deployment = name }
}

// WithDefaultModel sets the reranker model used when a call's Options
// leave Model empty.
func WithDefaultModel(modelID string) Option {
	return func(s *Service) { s.defaultModel = modelID }
}

// NewService builds a RerankService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	s := &Service{}
	for _, opt := range opts {
		opt(s)
	}

	core := sdk.TransportCore()

	clientOpts := core.ClientOpts
	if s.backpressure != nil {
		// The gate runs innermost so every retry attempt re-acquires a
		// slot and respects the shared shed pause.
		clientOpts = append(clientOpts[:len(clientOpts):len(clientOpts)],
			connect.WithInterceptors(transport.NewBackpressureInterceptor(s.backpressure.Gate())))
	}

	s.rpc = rerankconnect.NewRerankServiceClient(core.HTTPClient, core.BaseURL, clientOpts...)

	return s
}

// Options tunes a Rerank call. Nil keeps all defaults.
type Options struct {
	// Model overrides the service default reranker model.
	Model string
	// InferenceBackend pins the local inference engine.
	InferenceBackend InferenceBackend
	// TopN returns only the N best documents (0 = all).
	TopN int
	// MaxTokens overrides the model's input truncation limit.
	MaxTokens int
	// ReturnDocuments echoes the document texts in the results.
	ReturnDocuments bool
}

// Rerank calls RerankService/Rerank: scores every document against the
// query and returns results sorted by descending relevance.
func (s *Service) Rerank(ctx context.Context, query string, documents []string, opts *Options) (*Response, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	if opts == nil {
		opts = &Options{}
	}

	modelID := opts.Model
	if modelID == "" {
		modelID = s.defaultModel
	}

	model, err := ModelRef{ModelID: modelID, InferenceBackend: opts.InferenceBackend}.toProto()
	if err != nil {
		return nil, err
	}

	msg := &rerankpb.RerankRequest{
		Query:     query,
		Documents: documents,
		Model:     model,
	}
	if opts.TopN > 0 || opts.MaxTokens > 0 || opts.ReturnDocuments {
		msg.Options = &rerankpb.RerankOptions{
			ReturnDocuments: opts.ReturnDocuments,
		}
		if opts.TopN > 0 {
			msg.Options.TopN = protoconv.Int32Ptr(&opts.TopN)
		}

		if opts.MaxTokens > 0 {
			msg.Options.MaxTokens = protoconv.Int32Ptr(&opts.MaxTokens)
		}
	}

	resp, err := s.rpc.Rerank(ctx, connect.NewRequest(msg))
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
func (s *Service) ListModels(ctx context.Context) ([]ModelInfo, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	resp, err := s.rpc.ListModels(ctx, connect.NewRequest(&emptypb.Empty{}))
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
