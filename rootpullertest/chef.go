package rootpullertest

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/entwico/rootpuller-sdk/chef"
	"github.com/entwico/rootpuller-sdk/common"
	chefpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chef"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/chef/chefconnect"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
)

// Chef is a facade-typed fake DocumentProcessingService. Nil fields fall
// back to canned defaults: ProcessText echoes the text, ProcessMarkdown
// echoes the text as one chunk, ProcessTable returns one markdown table,
// and ListTokenizers returns the builtin character and word tokenizers.
type Chef struct {
	ProcessTextFunc     func(text string) (*chef.TextResult, error)
	ProcessMarkdownFunc func(text string) (*chef.MarkdownResult, error)
	Tables              []chef.MarkdownTable
	Tokenizers          []chef.TokenizerInfo
}

func (f *Chef) register(mux *http.ServeMux) {
	mux.Handle(chefconnect.NewDocumentProcessingServiceHandler(&chefHandler{fake: f}))
}

type chefHandler struct {
	chefconnect.UnimplementedDocumentProcessingServiceHandler

	fake *Chef
}

func (h *chefHandler) ProcessText(_ context.Context, req *connect.Request[chefpb.ProcessTextRequest]) (*connect.Response[chefpb.ProcessTextResponse], error) {
	processText := h.fake.ProcessTextFunc
	if processText == nil {
		processText = func(text string) (*chef.TextResult, error) {
			return &chef.TextResult{Content: text, ID: "text-1"}, nil
		}
	}

	result, err := processText(req.Msg.GetText())
	if err != nil {
		return nil, err
	}

	metadata, err := toProtoMetadata(result.Metadata)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&chefpb.ProcessTextResponse{
		Content:  result.Content,
		Id:       result.ID,
		Metadata: metadata,
	}), nil
}

func (h *chefHandler) ProcessTable(_ context.Context, _ *connect.Request[chefpb.ProcessTableRequest]) (*connect.Response[chefpb.ProcessTableResponse], error) {
	tables := h.fake.Tables
	if tables == nil {
		tables = []chef.MarkdownTable{{Content: "| a | b |\n| 1 | 2 |", EndIndex: 18, TokenCount: 8}}
	}

	return connect.NewResponse(&chefpb.ProcessTableResponse{Tables: toProtoTables(tables)}), nil
}

func (h *chefHandler) ProcessMarkdown(_ context.Context, req *connect.Request[chefpb.ProcessMarkdownRequest]) (*connect.Response[chefpb.ProcessMarkdownResponse], error) {
	processMarkdown := h.fake.ProcessMarkdownFunc
	if processMarkdown == nil {
		processMarkdown = func(text string) (*chef.MarkdownResult, error) {
			return &chef.MarkdownResult{
				Content: text,
				ID:      "markdown-1",
				Chunks:  []common.TextChunk{{Text: text, EndIndex: len(text), TokenCount: len(text)}},
			}, nil
		}
	}

	result, err := processMarkdown(req.Msg.GetText())
	if err != nil {
		return nil, err
	}

	metadata, err := toProtoMetadata(result.Metadata)
	if err != nil {
		return nil, err
	}

	resp := &chefpb.ProcessMarkdownResponse{
		Content:  result.Content,
		Id:       result.ID,
		Tables:   toProtoTables(result.Tables),
		Metadata: metadata,
	}
	for _, b := range result.CodeBlocks {
		resp.CodeBlocks = append(resp.CodeBlocks, &chefpb.CodeBlock{
			Content:    b.Content,
			Language:   b.Language,
			StartIndex: int32(b.StartIndex), //nolint:gosec // test-fixture indexes fit int32
			EndIndex:   int32(b.EndIndex),   //nolint:gosec // test-fixture indexes fit int32
			TokenCount: int32(b.TokenCount), //nolint:gosec // test-fixture counts fit int32
		})
	}

	for _, img := range result.Images {
		resp.Images = append(resp.Images, &chefpb.ImageRef{
			Url:        img.URL,
			AltText:    img.AltText,
			StartIndex: int32(img.StartIndex), //nolint:gosec // test-fixture indexes fit int32
			EndIndex:   int32(img.EndIndex),   //nolint:gosec // test-fixture indexes fit int32
		})
	}

	for _, c := range result.Chunks {
		resp.Chunks = append(resp.Chunks, &commonpb.TextChunk{
			Text:       c.Text,
			StartIndex: int32(c.StartIndex), //nolint:gosec // test-fixture indexes fit int32
			EndIndex:   int32(c.EndIndex),   //nolint:gosec // test-fixture indexes fit int32
			TokenCount: int32(c.TokenCount), //nolint:gosec // test-fixture counts fit int32
		})
	}

	return connect.NewResponse(resp), nil
}

func (h *chefHandler) ListTokenizers(_ context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[chefpb.ListTokenizersResponse], error) {
	tokenizers := h.fake.Tokenizers
	if tokenizers == nil {
		tokenizers = []chef.TokenizerInfo{
			{ID: "character", Type: "builtin", Description: "Character tokenizer"},
			{ID: "word", Type: "builtin", Description: "Word tokenizer"},
		}
	}

	resp := &chefpb.ListTokenizersResponse{}
	for _, t := range tokenizers {
		resp.Tokenizers = append(resp.Tokenizers, &chefpb.TokenizerInfo{
			Id:          t.ID,
			Type:        t.Type,
			Description: t.Description,
		})
	}

	return connect.NewResponse(resp), nil
}

func toProtoTables(tables []chef.MarkdownTable) []*chefpb.MarkdownTable {
	out := make([]*chefpb.MarkdownTable, len(tables))
	for i, t := range tables {
		out[i] = &chefpb.MarkdownTable{
			Content:    t.Content,
			Alias:      t.Alias,
			StartIndex: int32(t.StartIndex), //nolint:gosec // test-fixture indexes fit int32
			EndIndex:   int32(t.EndIndex),   //nolint:gosec // test-fixture indexes fit int32
			TokenCount: int32(t.TokenCount), //nolint:gosec // test-fixture counts fit int32
		}
	}

	return out
}

func toProtoMetadata(m map[string]any) (*structpb.Struct, error) {
	if m == nil {
		return nil, nil
	}

	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return s, nil
}
