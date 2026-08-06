// Package embedding wraps
// com.entwico.rootpuller.embedding.VectorEmbeddingService: batch and
// streamed text embedding against local ONNX/fastembed models or cloud
// embedding APIs, plus model discovery.
package embedding

import (
	"context"
	"fmt"
	"iter"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	embeddingpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/embedding"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/embedding/embeddingconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the VectorEmbeddingService.
type Service struct {
	rpc          embeddingconnect.VectorEmbeddingServiceClient
	deployment   string
	defaultModel ModelRef
}

// Option configures a Service at construction.
type Option func(*Service)

// WithDeployment sends the rootpuller-deployment routing header (e.g.
// "local", "cloudrun") on every call. A per-call
// rootpullersdk.ContextWithDeployment value still wins.
func WithDeployment(name string) Option {
	return func(s *Service) { s.deployment = name }
}

// WithDefaultModel sets the embedding model used when a call's Options
// leave Model empty.
func WithDefaultModel(model ModelRef) Option {
	return func(s *Service) { s.defaultModel = model }
}

// NewService builds a VectorEmbeddingService client on the sdk
// connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	core := sdk.TransportCore()

	s := &Service{rpc: embeddingconnect.NewVectorEmbeddingServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Options tunes an Embed or EmbedStream call. Nil keeps all defaults.
type Options struct {
	// Model overrides the service default embedding model.
	Model ModelRef
	// Mode selects the embedding representation (dense, sparse, ...).
	Mode Mode
	// Task tunes the embedding for its downstream use.
	Task Task
	// Dimensions truncates MRL-capable dense embeddings (0 = model
	// native dimension).
	Dimensions int
	// MaxTokens overrides the model's input truncation limit.
	MaxTokens int
	// Normalize L2-normalizes dense vectors server-side.
	Normalize bool
}

// Embed calls VectorEmbeddingService/Embed: embeds all inputs in one
// round trip. For large ingestion batches where results should arrive
// incrementally, use EmbedStream.
func (s *Service) Embed(ctx context.Context, inputs []Input, opts *Options) (*Response, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	msg, err := s.buildRequest(inputs, opts)
	if err != nil {
		return nil, err
	}

	resp, err := s.rpc.Embed(ctx, connect.NewRequest(msg))
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
func (s *Service) EmbedStream(ctx context.Context, inputs []Input, opts *Options) iter.Seq2[StreamResult, error] {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	msg, err := s.buildRequest(inputs, opts)
	if err != nil {
		return func(yield func(StreamResult, error) bool) {
			yield(StreamResult{}, err)
		}
	}

	stream, serr := s.rpc.EmbedStream(ctx, connect.NewRequest(msg))
	if serr != nil {
		wrapped := transport.WrapError(serr, embeddingconnect.VectorEmbeddingServiceEmbedStreamProcedure)

		return func(yield func(StreamResult, error) bool) {
			yield(StreamResult{}, wrapped)
		}
	}

	return streamio.EventSeq(stream, embeddingconnect.VectorEmbeddingServiceEmbedStreamProcedure, nil,
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
func (s *Service) ListModels(ctx context.Context) ([]ModelInfo, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	resp, err := s.rpc.ListModels(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, embeddingconnect.VectorEmbeddingServiceListModelsProcedure)
	}

	models := make([]ModelInfo, len(resp.Msg.GetModels()))
	for i, m := range resp.Msg.GetModels() {
		models[i] = modelInfoFromProto(m)
	}

	return models, nil
}

// buildRequest resolves the service defaults into opts and converts the
// call to the wire request.
func (s *Service) buildRequest(inputs []Input, opts *Options) (*embeddingpb.EmbedRequest, error) {
	if opts == nil {
		opts = &Options{}
	}

	modelRef := opts.Model
	if modelRef == (ModelRef{}) {
		modelRef = s.defaultModel
	}

	model, err := modelRef.toProto()
	if err != nil {
		return nil, err
	}

	mode, ok := modeToProto[opts.Mode]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown embedding mode %q", opts.Mode))
	}

	task, ok := taskToProto[opts.Task]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown embedding task %q", opts.Task))
	}

	pbInputs := make([]*embeddingpb.TextInput, len(inputs))
	for i, in := range inputs {
		pbInputs[i] = &embeddingpb.TextInput{Content: in.Content, Metadata: in.Metadata}
	}

	msg := &embeddingpb.EmbedRequest{Inputs: pbInputs, Model: model, Mode: mode, Task: task}
	if opts.Dimensions > 0 || opts.MaxTokens > 0 || opts.Normalize {
		msg.Options = &embeddingpb.EmbeddingOptions{Normalize: opts.Normalize}
		if opts.Dimensions > 0 {
			msg.Options.Dimensions = protoconv.Int32Ptr(&opts.Dimensions)
		}

		if opts.MaxTokens > 0 {
			msg.Options.MaxTokens = protoconv.Int32Ptr(&opts.MaxTokens)
		}
	}

	return msg, nil
}
