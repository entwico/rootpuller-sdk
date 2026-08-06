package chef

import (
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	chefpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chef"
)

// TableSourceType selects the input format for ProcessTable. The zero
// value keeps the server default (markdown).
type TableSourceType string

const (
	TableSourceTypeDefault  TableSourceType = ""
	TableSourceTypeCSV      TableSourceType = "csv"
	TableSourceTypeMarkdown TableSourceType = "markdown"
)

func (t TableSourceType) toProto() (*chefpb.TableSourceType, error) {
	switch t {
	case TableSourceTypeDefault:
		return nil, nil
	case TableSourceTypeCSV:
		v := chefpb.TableSourceType_TABLE_SOURCE_TYPE_CSV

		return &v, nil
	case TableSourceTypeMarkdown:
		v := chefpb.TableSourceType_TABLE_SOURCE_TYPE_MARKDOWN

		return &v, nil
	default:
		return nil, invalidArgument(fmt.Sprintf("unknown table source type %q", t))
	}
}

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// MarkdownTable is one table extracted from a document, rendered as
// markdown. Indexes are offsets into the source text.
type MarkdownTable struct {
	Content    string
	Alias      *string // placeholder alias in the processed text, if any
	StartIndex int
	EndIndex   int
	TokenCount int
}

// CodeBlock is one fenced code block extracted from a markdown document.
type CodeBlock struct {
	Content    string
	Language   *string // fence language tag, if any
	StartIndex int
	EndIndex   int
	TokenCount int
}

// ImageRef is one image reference extracted from a markdown document.
type ImageRef struct {
	URL        string
	AltText    *string
	StartIndex int
	EndIndex   int
}

// TokenizerInfo describes one tokenizer available for markdown chunk
// token counting; pass ID as MarkdownOptions.Tokenizer.
type TokenizerInfo struct {
	ID          string // chonkie tokenizer name, e.g. "character" or "word"
	Type        string // tokenizer category, currently "builtin"
	Description string // short human-readable label
}

// TextResult is the outcome of ProcessText.
type TextResult struct {
	Content string
	ID      string
	// Metadata carries server-side processing details; nil when the
	// server attached none.
	Metadata map[string]any
}

// MarkdownResult is the outcome of ProcessMarkdown.
type MarkdownResult struct {
	Content    string
	ID         string
	Tables     []MarkdownTable
	CodeBlocks []CodeBlock
	Images     []ImageRef
	Chunks     []rootpullersdk.TextChunk
	// Metadata carries server-side processing details; nil when the
	// server attached none.
	Metadata map[string]any
}

func fromProtoTables(tables []*chefpb.MarkdownTable) []MarkdownTable {
	out := make([]MarkdownTable, len(tables))
	for i, t := range tables {
		out[i] = MarkdownTable{
			Content:    t.GetContent(),
			Alias:      copyPtr(t.Alias),
			StartIndex: int(t.GetStartIndex()),
			EndIndex:   int(t.GetEndIndex()),
			TokenCount: int(t.GetTokenCount()),
		}
	}

	return out
}

func fromProtoCodeBlocks(blocks []*chefpb.CodeBlock) []CodeBlock {
	out := make([]CodeBlock, len(blocks))
	for i, b := range blocks {
		out[i] = CodeBlock{
			Content:    b.GetContent(),
			Language:   copyPtr(b.Language),
			StartIndex: int(b.GetStartIndex()),
			EndIndex:   int(b.GetEndIndex()),
			TokenCount: int(b.GetTokenCount()),
		}
	}

	return out
}

func fromProtoImages(images []*chefpb.ImageRef) []ImageRef {
	out := make([]ImageRef, len(images))
	for i, img := range images {
		out[i] = ImageRef{
			URL:        img.GetUrl(),
			AltText:    copyPtr(img.AltText),
			StartIndex: int(img.GetStartIndex()),
			EndIndex:   int(img.GetEndIndex()),
		}
	}

	return out
}

func fromProtoTokenizers(tokenizers []*chefpb.TokenizerInfo) []TokenizerInfo {
	out := make([]TokenizerInfo, len(tokenizers))
	for i, t := range tokenizers {
		out[i] = TokenizerInfo{
			ID:          t.GetId(),
			Type:        t.GetType(),
			Description: t.GetDescription(),
		}
	}

	return out
}

// fromProtoMetadata maps the optional google.protobuf.Struct metadata to
// a plain map; a nil proto struct stays a nil map.
func fromProtoMetadata(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}

	return s.AsMap()
}

// copyPtr detaches an optional proto field from the message it came from.
func copyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}

	v := *p

	return &v
}
