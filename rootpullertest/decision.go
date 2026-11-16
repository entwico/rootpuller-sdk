package rootpullertest

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/decision"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	decisionpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/decision"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/decision/decisionconnect"
)

// Decision is a facade-typed fake DecisionService. DecideFunc receives the
// state (a string, map[string]any or []any, as decoded from JSON) and the
// questions, and returns the answers; a nil DecideFunc answers every
// question with a canned value: a choice picks its alphabetically first
// label with probability 1, a score picks level 0, and a noul returns 0.5.
// The response echoes the request model. DecideImagesFunc, when set, wins
// over DecideFunc and also receives the request's images, inlined or
// streamed through DecideUpload alike. ListModels serves Models; when nil it
// serves one canned loaded Laya model.
type Decision struct {
	DecideFunc       func(state any, questions map[string]decision.Question) (map[string]decision.Answer, error)
	DecideImagesFunc func(state any, questions map[string]decision.Question, images map[string]rootpullersdk.File) (map[string]decision.Answer, error)
	Models           []decision.ModelInfo
}

func (f *Decision) register(mux *http.ServeMux) {
	mux.Handle(decisionconnect.NewDecisionServiceHandler(&decisionHandler{fake: f}))
}

type decisionHandler struct {
	decisionconnect.UnimplementedDecisionServiceHandler

	fake *Decision
}

func (h *decisionHandler) Decide(_ context.Context, req *connect.Request[decisionpb.DecideRequest]) (*connect.Response[decisionpb.DecideResponse], error) {
	images := make(map[string]rootpullersdk.File, len(req.Msg.GetImages()))
	for ref, img := range req.Msg.GetImages() {
		images[ref] = rootpullersdk.File{Name: ref, MIMEType: img.GetMimeType(), Data: img.GetData()}
	}

	resp, err := h.answer(req.Msg, images)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func (h *decisionHandler) DecideUpload(_ context.Context, stream *connect.ClientStream[decisionpb.DecideUploadRequest]) (*connect.Response[decisionpb.DecideUploadResponse], error) {
	var params *decisionpb.DecideRequest

	images := map[string]rootpullersdk.File{}

	for stream.Receive() {
		if p := stream.Msg().GetParams(); p != nil {
			params = p

			for ref, img := range p.GetImages() {
				images[ref] = rootpullersdk.File{Name: ref, MIMEType: img.GetMimeType(), Data: img.GetData()}
			}

			continue
		}

		chunk := stream.Msg().GetImage()
		img := images[chunk.GetName()]
		img.Name, img.MIMEType = chunk.GetName(), chunk.GetMimeType()
		img.Data = append(img.Data, chunk.GetData()...)
		images[chunk.GetName()] = img
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	if params == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, nil)
	}

	resp, err := h.answer(params, images)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&decisionpb.DecideUploadResponse{Response: resp}), nil
}

func (h *decisionHandler) ListModels(_ context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[decisionpb.ListDecisionModelsResponse], error) {
	models := h.fake.Models
	if models == nil {
		models = []decision.ModelInfo{{
			Model:             decision.ModelRef{Provider: decision.ProviderLaya, ModelID: "convaiinnovations/laya/multilingual"},
			MaxTokens:         1024,
			MaxQuestionTokens: 256,
			MaxOptions:        255,
			Loaded:            true,
			Description:       "Laya multilingual (mmBERT-base, 100+ languages)",
		}}
	}

	resp := &decisionpb.ListDecisionModelsResponse{}
	for _, m := range models {
		resp.Models = append(resp.Models, &decisionpb.DecisionModelInfo{
			Model: &decisionpb.DecisionModelRef{
				Provider: decisionProviderToProto(m.Model.Provider),
				ModelId:  m.Model.ModelID,
			},
			MaxTokens:         int32(m.MaxTokens),         //nolint:gosec // test-fixture limits fit int32
			MaxQuestionTokens: int32(m.MaxQuestionTokens), //nolint:gosec // test-fixture limits fit int32
			MaxOptions:        int32(m.MaxOptions),        //nolint:gosec // test-fixture limits fit int32
			Loaded:            m.Loaded,
			Description:       m.Description,
			SupportsImages:    m.SupportsImages,
		})
	}

	return connect.NewResponse(resp), nil
}

func (h *decisionHandler) answer(req *decisionpb.DecideRequest, images map[string]rootpullersdk.File) (*decisionpb.DecideResponse, error) {
	questions := make(map[string]decision.Question, len(req.GetQuestions()))
	for key, q := range req.GetQuestions() {
		questions[key] = decisionQuestionFromProto(q)
	}

	state := req.GetState().AsInterface()

	answers := cannedDecide(questions)

	var err error

	switch {
	case h.fake.DecideImagesFunc != nil:
		answers, err = h.fake.DecideImagesFunc(state, questions, images)
	case h.fake.DecideFunc != nil:
		answers, err = h.fake.DecideFunc(state, questions)
	}

	if err != nil {
		return nil, err
	}

	resp := &decisionpb.DecideResponse{
		Answers: make(map[string]*decisionpb.Answer, len(answers)),
		Model:   req.GetModel(),
		Usage:   &commonpb.Usage{InputTokens: 42},
	}
	for key, a := range answers {
		resp.Answers[key] = decisionAnswerToProto(a)
	}

	return resp, nil
}

func cannedDecide(questions map[string]decision.Question) map[string]decision.Answer {
	answers := make(map[string]decision.Answer, len(questions))
	for key, q := range questions {
		switch {
		case q.Choice != nil:
			first := ""
			for label := range q.Choice.Options {
				if first == "" || label < first {
					first = label
				}
			}

			probabilities := make(map[string]float32, len(q.Choice.Options))
			for label := range q.Choice.Options {
				probabilities[label] = 0
			}

			probabilities[first] = 1

			answers[key] = decision.Answer{Kind: decision.KindChoice, Choice: &decision.ChoiceAnswer{
				Choice:        first,
				Probabilities: probabilities,
				Confidence:    1,
			}}
		case q.Score != nil:
			probabilities := make([]float32, len(q.Score.Levels))
			if len(probabilities) > 0 {
				probabilities[0] = 1
			}

			answers[key] = decision.Answer{Kind: decision.KindScore, Score: &decision.ScoreAnswer{
				Probabilities: probabilities,
				Confidence:    1,
			}}
		case q.Noul != nil:
			answers[key] = decision.Answer{Kind: decision.KindNoul, Noul: &decision.NoulAnswer{Probability: 0.5}}
		}
	}

	return answers
}

func decisionQuestionFromProto(q *decisionpb.Question) decision.Question {
	switch v := q.GetKind().(type) {
	case *decisionpb.Question_Choice:
		return decision.Choice(v.Choice.GetInstructions(), v.Choice.GetOptions())
	case *decisionpb.Question_Score:
		return decision.Score(v.Score.GetInstructions(), v.Score.GetLevels()...)
	case *decisionpb.Question_Noul:
		if c := v.Noul.GetCriteria(); c != nil {
			return decision.NoulWithCriteria(v.Noul.GetInstructions(), c.GetWhenTrue(), c.GetWhenFalse())
		}

		return decision.Noul(v.Noul.GetInstructions())
	default:
		return decision.Question{}
	}
}

func decisionAnswerToProto(a decision.Answer) *decisionpb.Answer {
	switch {
	case a.Choice != nil:
		return &decisionpb.Answer{Kind: &decisionpb.Answer_Choice{Choice: &decisionpb.ChoiceAnswer{
			Choice:        a.Choice.Choice,
			Probabilities: a.Choice.Probabilities,
			Confidence:    a.Choice.Confidence,
		}}}
	case a.Score != nil:
		return &decisionpb.Answer{Kind: &decisionpb.Answer_Score{Score: &decisionpb.ScoreAnswer{
			Score:         a.Score.Score,
			Probabilities: a.Score.Probabilities,
			Confidence:    a.Score.Confidence,
		}}}
	case a.Noul != nil:
		return &decisionpb.Answer{Kind: &decisionpb.Answer_Noul{Noul: &decisionpb.NoulAnswer{Probability: a.Noul.Probability}}}
	default:
		return &decisionpb.Answer{}
	}
}

func decisionProviderToProto(p decision.Provider) decisionpb.DecisionProvider {
	switch p {
	case decision.ProviderLaya:
		return decisionpb.DecisionProvider_DECISION_PROVIDER_LAYA
	case decision.ProviderJev:
		return decisionpb.DecisionProvider_DECISION_PROVIDER_JEV
	case decision.ProviderSelfHosted:
		return decisionpb.DecisionProvider_DECISION_PROVIDER_SELF_HOSTED
	case decision.ProviderDefault:
		return decisionpb.DecisionProvider_DECISION_PROVIDER_UNSPECIFIED
	}

	return decisionpb.DecisionProvider_DECISION_PROVIDER_UNSPECIFIED
}
