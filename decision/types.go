package decision

import (
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/internal/apierr"
	decisionpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/decision"
)

// Provider selects the decision backend. The zero value lets the gateway
// pick its configured default backend.
type Provider string

const (
	ProviderDefault Provider = ""
	ProviderLaya    Provider = "laya" // Laya, open weights, served locally by the rootpuller worker
	ProviderJev     Provider = "jev"  // TypeSafe Jev, hosted API
)

var providerToProto = map[Provider]decisionpb.DecisionProvider{
	ProviderDefault: decisionpb.DecisionProvider_DECISION_PROVIDER_UNSPECIFIED,
	ProviderLaya:    decisionpb.DecisionProvider_DECISION_PROVIDER_LAYA,
	ProviderJev:     decisionpb.DecisionProvider_DECISION_PROVIDER_JEV,
}

func (p Provider) toProto() (decisionpb.DecisionProvider, error) {
	v, ok := providerToProto[p]
	if !ok {
		return 0, invalidArgument(fmt.Sprintf("unknown provider %q", p))
	}

	return v, nil
}

func providerFromProto(v decisionpb.DecisionProvider) Provider {
	switch v {
	case decisionpb.DecisionProvider_DECISION_PROVIDER_UNSPECIFIED:
		return ProviderDefault
	case decisionpb.DecisionProvider_DECISION_PROVIDER_LAYA:
		return ProviderLaya
	case decisionpb.DecisionProvider_DECISION_PROVIDER_JEV:
		return ProviderJev
	default:
		// unknown value from a newer server: preserve it in the same style
		// as the constants above
		name := strings.TrimPrefix(v.String(), "DECISION_PROVIDER_")

		return Provider(strings.ReplaceAll(strings.ToLower(name), "_", "-"))
	}
}

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// ModelRef identifies the provider and model that answer a request, e.g.
// {Provider: ProviderLaya, ModelID: "convaiinnovations/laya/multilingual"}.
// An empty ModelID uses the provider's default model; the zero value uses
// the gateway's default provider and its default model.
type ModelRef struct {
	Provider Provider
	ModelID  string
}

func (m ModelRef) toProto() (*decisionpb.DecisionModelRef, error) {
	provider, err := m.Provider.toProto()
	if err != nil {
		return nil, err
	}

	return &decisionpb.DecisionModelRef{Provider: provider, ModelId: m.ModelID}, nil
}

func modelRefFromProto(m *decisionpb.DecisionModelRef) ModelRef {
	if m == nil {
		return ModelRef{}
	}

	return ModelRef{Provider: providerFromProto(m.GetProvider()), ModelID: m.GetModelId()}
}

// Kind is the type of a question and of its answer.
type Kind string

const (
	KindChoice Kind = "choice" // pick one option out of a labelled set
	KindScore  Kind = "score"  // rate on an ordered rubric
	KindNoul   Kind = "noul"   // probability that a yes/no statement holds
)

// ChoiceQuestion picks exactly one option out of a labelled set. Options
// map each label (2–255 of them) to a description of when it applies; an
// empty description is allowed.
type ChoiceQuestion struct {
	Instructions string
	Options      map[string]string
}

// ScoreQuestion rates the state on an ordered rubric. Levels (2–10) run
// from lowest (index 0) to highest.
type ScoreQuestion struct {
	Instructions string
	Levels       []string
}

// NoulCriteria describes when a noul statement counts as true and when as
// false. Both descriptions must be set.
type NoulCriteria struct {
	WhenTrue  string
	WhenFalse string
}

// NoulQuestion asks for the probability that a yes/no statement holds.
// Criteria is optional.
type NoulQuestion struct {
	Instructions string
	Criteria     *NoulCriteria
}

// Question is one typed question. Exactly one of Choice, Score and Noul
// must be set; the constructors Choice, Score, Noul and NoulWithCriteria
// guarantee that.
type Question struct {
	Choice *ChoiceQuestion
	Score  *ScoreQuestion
	Noul   *NoulQuestion
}

// Choice returns a question that picks one of the labelled options.
func Choice(instructions string, options map[string]string) Question {
	return Question{Choice: &ChoiceQuestion{Instructions: instructions, Options: options}}
}

// Score returns a question that rates the state on the given levels,
// lowest first.
func Score(instructions string, levels ...string) Question {
	return Question{Score: &ScoreQuestion{Instructions: instructions, Levels: levels}}
}

// Noul returns a question asking for the probability that the statement in
// instructions holds.
func Noul(instructions string) Question {
	return Question{Noul: &NoulQuestion{Instructions: instructions}}
}

// NoulWithCriteria returns a noul question whose true and false cases are
// described explicitly.
func NoulWithCriteria(instructions, whenTrue, whenFalse string) Question {
	return Question{Noul: &NoulQuestion{
		Instructions: instructions,
		Criteria:     &NoulCriteria{WhenTrue: whenTrue, WhenFalse: whenFalse},
	}}
}

func questionToProto(key string, q Question) (*decisionpb.Question, error) {
	set := 0

	for _, isSet := range []bool{q.Choice != nil, q.Score != nil, q.Noul != nil} {
		if isSet {
			set++
		}
	}

	if set != 1 {
		return nil, invalidArgument(fmt.Sprintf("question %q must set exactly one of Choice, Score, Noul", key))
	}

	switch {
	case q.Choice != nil:
		return &decisionpb.Question{Kind: &decisionpb.Question_Choice{Choice: &decisionpb.ChoiceQuestion{
			Instructions: q.Choice.Instructions,
			Options:      q.Choice.Options,
		}}}, nil
	case q.Score != nil:
		return &decisionpb.Question{Kind: &decisionpb.Question_Score{Score: &decisionpb.ScoreQuestion{
			Instructions: q.Score.Instructions,
			Levels:       q.Score.Levels,
		}}}, nil
	default:
		noul := &decisionpb.NoulQuestion{Instructions: q.Noul.Instructions}
		if c := q.Noul.Criteria; c != nil {
			noul.Criteria = &decisionpb.NoulCriteria{WhenTrue: c.WhenTrue, WhenFalse: c.WhenFalse}
		}

		return &decisionpb.Question{Kind: &decisionpb.Question_Noul{Noul: noul}}, nil
	}
}

// ChoiceAnswer is the answer to a choice question. Probabilities hold every
// option label and sum to 1; Confidence is a 0–1 certainty to gate on.
type ChoiceAnswer struct {
	Choice        string
	Probabilities map[string]float32
	Confidence    float32
}

// ScoreAnswer is the answer to a score question. Score is the expected
// level index; Probabilities are indexed like the question's levels.
type ScoreAnswer struct {
	Score         float32
	Probabilities []float32
	Confidence    float32
}

// NoulAnswer is the answer to a noul question: the probability, 0–1, that
// the statement holds.
type NoulAnswer struct {
	Probability float32
}

// Answer is the answer to one question. Kind tells which of Choice, Score
// and Noul is set; it matches the question's kind.
type Answer struct {
	Kind   Kind
	Choice *ChoiceAnswer
	Score  *ScoreAnswer
	Noul   *NoulAnswer
}

func answerFromProto(a *decisionpb.Answer) Answer {
	switch v := a.GetKind().(type) {
	case *decisionpb.Answer_Choice:
		return Answer{Kind: KindChoice, Choice: &ChoiceAnswer{
			Choice:        v.Choice.GetChoice(),
			Probabilities: v.Choice.GetProbabilities(),
			Confidence:    v.Choice.GetConfidence(),
		}}
	case *decisionpb.Answer_Score:
		return Answer{Kind: KindScore, Score: &ScoreAnswer{
			Score:         v.Score.GetScore(),
			Probabilities: v.Score.GetProbabilities(),
			Confidence:    v.Score.GetConfidence(),
		}}
	case *decisionpb.Answer_Noul:
		return Answer{Kind: KindNoul, Noul: &NoulAnswer{Probability: v.Noul.GetProbability()}}
	default:
		return Answer{}
	}
}

// ModelInfo describes one decision model the gateway can serve. Loaded
// means Laya weights are present on the worker, or the gateway has a Jev
// API key configured.
type ModelInfo struct {
	Model ModelRef
	// MaxTokens is the total context limit (state plus one question); zero
	// if unknown.
	MaxTokens int
	// MaxQuestionTokens is the token budget for one question's
	// instructions and option texts; zero if unknown.
	MaxQuestionTokens int
	// MaxOptions is the maximum number of options of a choice question.
	MaxOptions  int
	Loaded      bool
	Description string
}

func modelInfoFromProto(m *decisionpb.DecisionModelInfo) ModelInfo {
	return ModelInfo{
		Model:             modelRefFromProto(m.GetModel()),
		MaxTokens:         int(m.GetMaxTokens()),
		MaxQuestionTokens: int(m.GetMaxQuestionTokens()),
		MaxOptions:        int(m.GetMaxOptions()),
		Loaded:            m.GetLoaded(),
		Description:       m.GetDescription(),
	}
}
