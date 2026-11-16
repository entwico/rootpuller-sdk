// Package decision wraps com.entwico.rootpuller.decision.DecisionService:
// typed questions (choice, score, noul) about a state, answered with
// calibrated probabilities in one forward pass by Laya (open weights,
// served locally by the rootpuller worker), TypeSafe Jev (hosted) or a
// self-hosted model, plus discovery of the available decision models.
// Requests may carry images for the models that accept them.
package decision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	decisionpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/decision"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/decision/decisionconnect"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// MaxInlineImageBytes is the largest total of image bytes sent inside a
// unary Decide; larger requests stream the images with DecideUpload, which
// is not bound by the transport's 4 MiB message limit.
const MaxInlineImageBytes = 3 << 20

// ImageRef returns a fresh reference for an image: random enough that it
// cannot occur in a state or a question by accident, which matters because
// references match as plain text.
func ImageRef() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	return "image-" + hex.EncodeToString(b)
}

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
		clientOpts = append(slices.Clip(clientOpts),
			connect.WithInterceptors(transport.NewBackpressureInterceptor(s.backpressure.Gate())))
	}

	s.rpc = decisionconnect.NewDecisionServiceClient(core.HTTPClient, core.BaseURL, clientOpts...)

	return s
}

// Options tunes a Decide call. Nil keeps all defaults.
type Options struct {
	// Model overrides the service default provider and model.
	Model ModelRef
	// Images map a reference to a PNG or JPEG image. Write the reference
	// wherever the image belongs: in the state, a question's instructions,
	// or an option or level description (see ImageRef). Only models whose
	// ModelInfo.SupportsImages is set accept them.
	Images map[string]rootpullersdk.File
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

	total := 0
	for _, img := range opts.Images {
		total += len(img.Data)
	}

	if total > MaxInlineImageBytes {
		return s.decideUpload(ctx, msg, opts.Images)
	}

	if len(opts.Images) > 0 {
		msg.Images = make(map[string]*decisionpb.DecisionImage, len(opts.Images))
		for ref, img := range opts.Images {
			msg.Images[ref] = &decisionpb.DecisionImage{MimeType: img.MIMEType, Data: img.Data}
		}
	}

	resp, err := s.rpc.Decide(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, transport.WrapError(err, decisionconnect.DecisionServiceDecideProcedure)
	}

	return responseFromProto(resp.Msg), nil
}

func responseFromProto(msg *decisionpb.DecideResponse) *Response {
	answers := msg.GetAnswers()

	out := &Response{
		Answers: make(map[string]Answer, len(answers)),
		Model:   modelRefFromProto(msg.GetModel()),
		Usage:   protoconv.FromProtoUsage(msg.GetUsage()),
	}
	for key, a := range answers {
		out.Answers[key] = answerFromProto(a)
	}

	return out
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

// decideUpload sends the request first and then every image in chunks named
// by its reference. A send that fails part-way cancels the call, so the
// server never answers on a truncated image.
func (s *Service) decideUpload(ctx context.Context, msg *decisionpb.DecideRequest, images map[string]rootpullersdk.File) (*Response, error) {
	procedure := decisionconnect.DecisionServiceDecideUploadProcedure

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream := s.rpc.DecideUpload(ctx)

	send := func(frame *decisionpb.DecideUploadRequest) error {
		if err := stream.Send(frame); err != nil {
			if errors.Is(err, io.EOF) {
				// the server ended the call; its status comes from the receive side
				_, rerr := stream.CloseAndReceive()

				return transport.WrapError(serverFirst(rerr, err), procedure)
			}

			cancel()

			return transport.WrapError(err, procedure)
		}

		return nil
	}

	if err := send(&decisionpb.DecideUploadRequest{Request: &decisionpb.DecideUploadRequest_Params{Params: msg}}); err != nil {
		return nil, err
	}

	for _, ref := range slices.Sorted(maps.Keys(images)) {
		img := images[ref]
		upload := rootpullersdk.UploadBytes(ref, img.MIMEType, img.Data)

		frames := streamio.FileChunkFrames(upload, func(c *commonpb.FileChunk) *decisionpb.DecideUploadRequest {
			return &decisionpb.DecideUploadRequest{Request: &decisionpb.DecideUploadRequest_Image{Image: c}}
		})

		for frame, err := range frames {
			if err != nil {
				cancel()

				return nil, err
			}

			if err := send(frame); err != nil {
				return nil, err
			}
		}
	}

	resp, err := stream.CloseAndReceive()
	if err != nil {
		return nil, transport.WrapError(err, procedure)
	}

	return responseFromProto(resp.Msg.GetResponse()), nil
}

// serverFirst prefers the server's status over the bare EOF of the send side.
func serverFirst(serverErr, sendErr error) error {
	if serverErr != nil {
		return serverErr
	}

	return sendErr
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
