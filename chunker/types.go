package chunker

import (
	"fmt"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/internal/apierr"
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
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}
