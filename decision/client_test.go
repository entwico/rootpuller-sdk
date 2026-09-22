package decision_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/decision"
	decisionpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/decision"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/decision/decisionconnect"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

const (
	layaModel = "convaiinnovations/laya/multilingual"
	billing   = "billing"
	urgent    = "urgent"
)

func newService(t *testing.T, baseURL string, opts ...decision.Option) *decision.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return decision.NewService(sdk, opts...)
}

type ticket struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func TestDecideRoundTrip(t *testing.T) {
	t.Parallel()

	questions := map[string]decision.Question{
		"department": decision.Choice("Which team handles this?", map[string]string{
			billing:     "payments, refunds",
			"technical": "bugs, outages",
		}),
		"severity": decision.Score("How severe is it?", "low", "medium", "high"),
		urgent:     decision.NoulWithCriteria("Is this urgent?", "blocks work", "can wait"),
	}

	tests := []struct {
		name      string
		state     any
		wantState any
	}{
		{name: "string", state: "payouts failing for 3 days", wantState: "payouts failing for 3 days"},
		{
			name:      "struct",
			state:     ticket{Subject: "Duplicate charge", Body: "charged twice"},
			wantState: map[string]any{"subject": "Duplicate charge", "body": "charged twice"},
		},
		{
			name:      "slice",
			state:     []map[string]string{{"role": "user", "text": "hi"}},
			wantState: []any{map[string]any{"role": "user", "text": "hi"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				gotState     any
				gotQuestions map[string]decision.Question
			)

			srv := rootpullertest.NewServer(t, &rootpullertest.Decision{
				DecideFunc: func(state any, qs map[string]decision.Question) (map[string]decision.Answer, error) {
					gotState = state
					gotQuestions = qs

					return map[string]decision.Answer{
						"department": {Kind: decision.KindChoice, Choice: &decision.ChoiceAnswer{
							Choice:        billing,
							Probabilities: map[string]float32{billing: 0.75, "technical": 0.25},
							Confidence:    0.5,
						}},
						"severity": {Kind: decision.KindScore, Score: &decision.ScoreAnswer{
							Score:         1.5,
							Probabilities: []float32{0, 0.5, 0.5},
							Confidence:    0.25,
						}},
						urgent: {Kind: decision.KindNoul, Noul: &decision.NoulAnswer{Probability: 0.875}},
					}, nil
				},
			})

			svc := newService(t, srv.URL)

			model := decision.ModelRef{Provider: decision.ProviderJev, ModelID: "jev-latest"}

			resp, err := svc.Decide(t.Context(), tt.state, questions, &decision.Options{Model: model})
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(gotState, tt.wantState) {
				t.Errorf("server saw state %#v, want %#v", gotState, tt.wantState)
			}

			if !reflect.DeepEqual(gotQuestions, questions) {
				t.Errorf("server saw questions %+v, want %+v", gotQuestions, questions)
			}

			dep := resp.Answers["department"]
			if dep.Kind != decision.KindChoice || dep.Choice.Choice != billing || dep.Choice.Probabilities["technical"] != 0.25 || dep.Choice.Confidence != 0.5 {
				t.Errorf("department = %+v", dep.Choice)
			}

			sev := resp.Answers["severity"]
			if sev.Kind != decision.KindScore || sev.Score.Score != 1.5 || !reflect.DeepEqual(sev.Score.Probabilities, []float32{0, 0.5, 0.5}) {
				t.Errorf("severity = %+v", sev.Score)
			}

			urg := resp.Answers[urgent]
			if urg.Kind != decision.KindNoul || urg.Noul.Probability != 0.875 {
				t.Errorf("urgent = %+v", urg.Noul)
			}

			if resp.Model != model {
				t.Errorf("Model = %+v, want %+v", resp.Model, model)
			}

			if resp.Usage.InputTokens != 42 {
				t.Errorf("Usage.InputTokens = %d, want 42", resp.Usage.InputTokens)
			}
		})
	}
}

func TestDecideCannedAnswers(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.Decision{})
	svc := newService(t, srv.URL)

	resp, err := svc.Decide(t.Context(), "text", map[string]decision.Question{
		"team":  decision.Choice("team?", map[string]string{"b": "", "a": ""}),
		"level": decision.Score("level?", "low", "high"),
		urgent:  decision.Noul("urgent?"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := resp.Answers["team"].Choice.Choice; got != "a" {
		t.Errorf("team choice = %q, want %q", got, "a")
	}

	if got := resp.Answers["level"].Score.Probabilities; !reflect.DeepEqual(got, []float32{1, 0}) {
		t.Errorf("level probabilities = %v, want [1 0]", got)
	}

	if got := resp.Answers[urgent].Noul.Probability; got != 0.5 {
		t.Errorf("urgent probability = %v, want 0.5", got)
	}
}

// capturingDecision records the proto request of each Decide call.
type capturingDecision struct {
	decisionconnect.UnimplementedDecisionServiceHandler

	requests chan *decisionpb.DecideRequest
}

func (h *capturingDecision) Decide(_ context.Context, req *connect.Request[decisionpb.DecideRequest]) (*connect.Response[decisionpb.DecideResponse], error) {
	h.requests <- req.Msg

	return connect.NewResponse(&decisionpb.DecideResponse{}), nil
}

func newCapturingServer(t *testing.T) (*rootpullertest.Server, chan *decisionpb.DecideRequest) {
	t.Helper()

	handler := &capturingDecision{requests: make(chan *decisionpb.DecideRequest, 1)}
	mux := http.NewServeMux()
	mux.Handle(decisionconnect.NewDecisionServiceHandler(handler))

	return rootpullertest.NewServerWithMux(t, mux), handler.requests
}

func TestDecideRequestShape(t *testing.T) {
	t.Parallel()

	srv, requests := newCapturingServer(t)
	svc := newService(t, srv.URL)

	_, err := svc.Decide(t.Context(), map[string]any{"subject": "hi"}, map[string]decision.Question{
		"team":  decision.Choice("team?", map[string]string{"a": "first"}),
		"level": decision.Score("level?", "low", "high"),
		urgent:  decision.Noul("urgent?"),
		"spam":  decision.NoulWithCriteria("spam?", "ads", "real mail"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	msg := <-requests

	if _, ok := msg.GetState().GetKind().(*structpb.Value_StructValue); !ok {
		t.Errorf("state kind = %T, want struct", msg.GetState().GetKind())
	}

	qs := msg.GetQuestions()
	if got := qs["team"].GetChoice().GetOptions()["a"]; got != "first" {
		t.Errorf("team option = %q", got)
	}

	if got := qs["level"].GetScore().GetLevels(); !reflect.DeepEqual(got, []string{"low", "high"}) {
		t.Errorf("level levels = %v", got)
	}

	if qs[urgent].GetNoul() == nil || qs[urgent].GetNoul().GetCriteria() != nil {
		t.Errorf("urgent = %v, want noul without criteria", qs[urgent])
	}

	if c := qs["spam"].GetNoul().GetCriteria(); c.GetWhenTrue() != "ads" || c.GetWhenFalse() != "real mail" {
		t.Errorf("spam criteria = %v", c)
	}

	if p := msg.GetModel().GetProvider(); p != decisionpb.DecisionProvider_DECISION_PROVIDER_UNSPECIFIED {
		t.Errorf("provider = %v, want unspecified when no default is set", p)
	}
}

func TestServiceDefaults(t *testing.T) {
	t.Parallel()

	srv, requests := newCapturingServer(t)
	svc := newService(t, srv.URL,
		decision.WithDefaultModel(decision.ModelRef{Provider: decision.ProviderLaya, ModelID: layaModel}))

	questions := map[string]decision.Question{"q": decision.Noul("statement?")}

	// the construction-time default applies when Options leave Model zero...
	if _, err := svc.Decide(t.Context(), "s", questions, nil); err != nil {
		t.Fatal(err)
	}

	got := (<-requests).GetModel()
	if got.GetProvider() != decisionpb.DecisionProvider_DECISION_PROVIDER_LAYA || got.GetModelId() != layaModel {
		t.Errorf("model = %v, want service default", got)
	}

	// ...and a per-call model wins over the default
	if _, err := svc.Decide(t.Context(), "s", questions, &decision.Options{Model: decision.ModelRef{Provider: decision.ProviderJev}}); err != nil {
		t.Fatal(err)
	}

	got = (<-requests).GetModel()
	if got.GetProvider() != decisionpb.DecisionProvider_DECISION_PROVIDER_JEV || got.GetModelId() != "" {
		t.Errorf("model = %v, want per-call override", got)
	}
}

func TestListModelsRoundTrip(t *testing.T) {
	t.Parallel()

	want := []decision.ModelInfo{
		{
			Model:             decision.ModelRef{Provider: decision.ProviderLaya, ModelID: layaModel},
			MaxTokens:         1024,
			MaxQuestionTokens: 256,
			MaxOptions:        255,
			Loaded:            true,
			Description:       "Laya multilingual",
		},
		{
			Model:             decision.ModelRef{Provider: decision.ProviderJev, ModelID: "jev-latest"},
			MaxTokens:         65536,
			MaxQuestionTokens: 32768,
			MaxOptions:        255,
			Loaded:            true,
			Description:       "TypeSafe Jev (hosted)",
		},
	}

	srv := rootpullertest.NewServer(t, &rootpullertest.Decision{Models: want})
	svc := newService(t, srv.URL)

	models, err := svc.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(models, want) {
		t.Errorf("models = %+v, want %+v", models, want)
	}
}

func TestInvalidInputFailsLocally(t *testing.T) {
	t.Parallel()

	// no server: local validation must fail before any dial
	svc := newService(t, "http://127.0.0.1:1")

	valid := map[string]decision.Question{"q": decision.Noul("statement?")}

	tests := []struct {
		name      string
		state     any
		questions map[string]decision.Question
		opts      *decision.Options
	}{
		{
			name:      "unknown provider",
			state:     "s",
			questions: valid,
			opts:      &decision.Options{Model: decision.ModelRef{Provider: decision.Provider("bogus")}},
		},
		{name: "number state", state: 42, questions: valid},
		{name: "unencodable state", state: make(chan int), questions: valid},
		{name: "question without kind", state: "s", questions: map[string]decision.Question{"q": {}}},
		{
			name:  "question with two kinds",
			state: "s",
			questions: map[string]decision.Question{"q": {
				Noul:  &decision.NoulQuestion{Instructions: "a"},
				Score: &decision.ScoreQuestion{Instructions: "b"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.Decide(t.Context(), tt.state, tt.questions, tt.opts)
			if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
