package chunker

import (
	"fmt"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/common"
	chunkerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chunker"
)

// Tokenizer selects the tokenizer used for token counting. The zero value
// keeps the server default (GPT-2 for most methods, character-based for
// recursive and slumber chunking).
type Tokenizer string

const (
	TokenizerDefault    Tokenizer = ""
	TokenizerCharacter  Tokenizer = "character"
	TokenizerWord       Tokenizer = "word"
	TokenizerGPT2       Tokenizer = "gpt2"
	TokenizerGPT2German Tokenizer = "gpt2-german"
)

var tokenizerToProto = map[Tokenizer]chunkerpb.TextTokenizer{
	TokenizerDefault:    chunkerpb.TextTokenizer_TEXT_TOKENIZER_UNSPECIFIED,
	TokenizerCharacter:  chunkerpb.TextTokenizer_TEXT_TOKENIZER_CHARACTER,
	TokenizerWord:       chunkerpb.TextTokenizer_TEXT_TOKENIZER_WORD,
	TokenizerGPT2:       chunkerpb.TextTokenizer_TEXT_TOKENIZER_GPT2,
	TokenizerGPT2German: chunkerpb.TextTokenizer_TEXT_TOKENIZER_GPT2_GERMAN,
}

func (t Tokenizer) toProto() (chunkerpb.TextTokenizer, error) {
	v, ok := tokenizerToProto[t]
	if !ok {
		return 0, invalidArgument(fmt.Sprintf("unknown tokenizer %q", t))
	}
	return v, nil
}

// DelimiterInclusion controls which chunk a sentence delimiter belongs
// to. The zero value keeps the server default (previous chunk).
type DelimiterInclusion string

const (
	DelimiterInclusionDefault DelimiterInclusion = ""
	DelimiterInclusionPrev    DelimiterInclusion = "prev"
	DelimiterInclusionNext    DelimiterInclusion = "next"
)

func (d DelimiterInclusion) toProto() (*chunkerpb.SentenceDelimiterInclusion, error) {
	switch d {
	case DelimiterInclusionDefault:
		return nil, nil
	case DelimiterInclusionPrev:
		v := chunkerpb.SentenceDelimiterInclusion_SENTENCE_DELIMITER_INCLUSION_PREV
		return &v, nil
	case DelimiterInclusionNext:
		v := chunkerpb.SentenceDelimiterInclusion_SENTENCE_DELIMITER_INCLUSION_NEXT
		return &v, nil
	default:
		return nil, invalidArgument(fmt.Sprintf("unknown delimiter inclusion %q", d))
	}
}

// Genie selects the LLM backend for slumber chunking. The zero value
// keeps the server default (Gemini).
type Genie string

const (
	GenieDefault Genie = ""
	GenieGemini  Genie = "gemini"
	GenieOpenAI  Genie = "openai"
)

func (g Genie) toProto() (*chunkerpb.ChunkSlumberRequest_Genie, error) {
	switch g {
	case GenieDefault:
		return nil, nil
	case GenieGemini:
		v := chunkerpb.ChunkSlumberRequest_GENIE_GEMINI
		return &v, nil
	case GenieOpenAI:
		v := chunkerpb.ChunkSlumberRequest_GENIE_OPENAI
		return &v, nil
	default:
		return nil, invalidArgument(fmt.Sprintf("unknown genie %q", g))
	}
}

func invalidArgument(msg string) error {
	return apierror.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// SemanticRequest configures ChunkSemantic. Nil pointer fields keep the
// server defaults noted per field.
type SemanticRequest struct {
	Texts []string
	// Model is the embedding model used for similarity; "" keeps the
	// server default (minishlab/potion-base-32M).
	Model                    string
	Threshold                *float32 // similarity threshold 0.0-1.0 (default 0.8)
	ChunkSize                *int     // max tokens per chunk (default 512)
	SimilarityWindow         *int     // window for similarity comparison (default 1)
	MinSentencesPerChunk     *int     // default 1
	MinCharactersPerSentence *int     // default 12
	Delimiters               []string // default [". ", "! ", "? ", "\n"]
	DelimiterInclusion       DelimiterInclusion
	SkipWindow               *int     // default 0
	FilterWindow             *int     // default 5
	FilterPolyorder          *int     // default 3
	FilterTolerance          *float32 // default 0.2
	Normalize                *common.NormalizeOptions
	MinCharactersPerChunk    *int // merge shorter chunks into a neighbour (default off)
}

// TokenRequest configures ChunkToken.
type TokenRequest struct {
	Texts        []string
	Tokenizer    Tokenizer
	ChunkSize    *int // max tokens per chunk (default 512)
	ChunkOverlap *int // token overlap between chunks (default 0)
	Normalize    *common.NormalizeOptions
}

// SentenceRequest configures ChunkSentence.
type SentenceRequest struct {
	Texts                    []string
	Tokenizer                Tokenizer
	ChunkSize                *int
	ChunkOverlap             *int
	MinSentencesPerChunk     *int
	MinCharactersPerSentence *int
	Approximate              *bool
	Delimiters               []string
	DelimiterInclusion       DelimiterInclusion
	Normalize                *common.NormalizeOptions
}

// CodeRequest configures ChunkCode.
type CodeRequest struct {
	Texts     []string
	Tokenizer Tokenizer
	ChunkSize *int
	// Language is the programming language ("" lets the server infer it).
	Language *string
}

// RecursiveRequest configures ChunkRecursive.
type RecursiveRequest struct {
	Texts                 []string
	Tokenizer             Tokenizer
	ChunkSize             *int
	MinCharactersPerChunk *int
	Recipe                *string // default "default"
	Lang                  *string // default "en"
	Normalize             *common.NormalizeOptions
}

// LateRequest configures ChunkLate.
type LateRequest struct {
	Texts []string
	// Model is the embedding model; "" keeps the server default.
	Model                 string
	ChunkSize             *int
	MinCharactersPerChunk *int
	Recipe                *string
	Lang                  *string
	Normalize             *common.NormalizeOptions
}

// NeuralRequest configures ChunkNeural.
type NeuralRequest struct {
	Texts []string
	// Model is the neural chunking model; "" keeps the server default.
	Model                 string
	MinCharactersPerChunk *int
	Normalize             *common.NormalizeOptions
}

// SlumberRequest configures ChunkSlumber (LLM-assisted chunking).
type SlumberRequest struct {
	Texts                 []string
	Tokenizer             Tokenizer
	ChunkSize             *int // default 1024
	Recipe                *string
	Lang                  *string
	CandidateSize         *int // default 128
	MinCharactersPerChunk *int // default 24
	Normalize             *common.NormalizeOptions
	Genie                 Genie
	Model                 *string // LLM model override
	BaseURL               *string // LLM endpoint override
}
