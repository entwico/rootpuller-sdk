// Package decision wraps com.entwico.rootpuller.decision.DecisionService:
// typed questions (choice, score, noul) about a state, answered with
// calibrated probabilities in one forward pass by Laya (open weights,
// served locally by the rootpuller worker) or TypeSafe Jev (hosted), plus
// discovery of the available decision models.
package decision

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	decisionpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/decision"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/decision/decisionconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the DecisionService.
type Service struct {
	rpc          decisionconnect.DecisionServiceClient
	deployment   string
	defaultModel ModelRef
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
// "local", "cloudrun") on every call. It selects the worker that serves
// Laya and is ignored for Jev. A per-call
// rootpullersdk.ContextWithDeployment value still wins.
func WithDeployment(name string) Option {
	return func(s *Service) { s.deployment = name }
}

// WithDefaultModel sets the provider and model used when a call's Options
// leave Model as the zero value.
func WithDefaultModel(model ModelRef) Option {
	return func(s *Service) { s.defaultModel = model }
}

// NewService builds a DecisionService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client, opts ...Option) *Service {
	s := &Service{}
	for _, opt := range opts {
		opt(s)
	}

	core := sdk.TransportCore()

	clientOpts := core.ClientOpts
	if s.backpressure != nil {
		// the gate runs innermost so every retry attempt re-acquires a
		// slot and respects the shared shed pause
		clientOpts = append(clientOpts[:len(clientOpts):len(clientOpts)],
			connect.WithInterceptors(transport.NewBackpressureInterceptor(s.backpressure.Gate())))
	}

	s.rpc = decisionconnect.NewDecisionServiceClient(core.HTTPClient, core.BaseURL, clientOpts...)

	return s
}

// Options tunes a Decide call. Nil keeps all defaults.
type Options struct {
	// Model overrides the service default provider and model.
	Model ModelRef
}

// Response is the outcome of Decide: one answer per question key, the
// provider and model that answered, and token usage (hosted providers
// also report an estimated cost).
type Response struct {
	Answers map[string]Answer
	Model   ModelRef
	Usage   rootpullersdk.Usage
}

// Decide calls DecisionService/Decide: answers every question against the
// same state in one round trip.
//
// state is the content the questions are about: a string for text, or any
// value that marshals to a JSON object or array (a map, a slice, or the
// caller's own struct). Other values fail locally with ErrInvalidArgument.
func (s *Service) Decide(ctx context.Context, state any, questions map[string]Question, opts *Options) (*Response, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

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

	stateValue, err := stateToProto(state)
	if err != nil {
		return nil, err
	}

	msg := &decisionpb.DecideRequest{
		Model:     model,
		State:     stateValue,
		Questions: make(map[string]*decisionpb.Question, len(questions)),
	}
	for key, q := range questions {
		pq, err := questionToProto(key, q)
		if err != nil {
			return nil, err
		}

		msg.Questions[key] = pq
	}

	resp, err := s.rpc.Decide(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, decisionconnect.DecisionServiceDecideProcedure)
	}

	answers := resp.Msg.GetAnswers()

	out := &Response{
		Answers: make(map[string]Answer, len(answers)),
		Model:   modelRefFromProto(resp.Msg.GetModel()),
		Usage:   protoconv.FromProtoUsage(resp.Msg.GetUsage()),
	}
	for key, a := range answers {
		out.Answers[key] = answerFromProto(a)
	}

	return out, nil
}

// ListModels calls DecisionService/ListModels: lists the decision models
// the gateway can serve, local models first.
func (s *Service) ListModels(ctx context.Context) ([]ModelInfo, error) {
	ctx = transport.EnsureDeployment(ctx, s.deployment)

	resp, err := s.rpc.ListModels(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, decisionconnect.DecisionServiceListModelsProcedure)
	}

	models := resp.Msg.GetModels()

	out := make([]ModelInfo, len(models))
	for i, m := range models {
		out[i] = modelInfoFromProto(m)
	}

	return out, nil
}

func stateToProto(state any) (*structpb.Value, error) {
	if text, ok := state.(string); ok {
		return structpb.NewStringValue(text), nil
	}

	// round-trip through JSON so callers can pass their own structs
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, invalidArgument(fmt.Sprintf("state is not JSON-encodable: %v", err))
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, invalidArgument(fmt.Sprintf("state is not JSON-encodable: %v", err))
	}

	value, err := structpb.NewValue(decoded)
	if err != nil {
		return nil, invalidArgument(fmt.Sprintf("state: %v", err))
	}

	switch value.GetKind().(type) {
	case *structpb.Value_StructValue, *structpb.Value_ListValue:
		return value, nil
	default:
		return nil, invalidArgument("state must be a string, a JSON object or a JSON array")
	}
}
