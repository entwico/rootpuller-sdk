package completion_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/completion"
	completionpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/completion"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/completion/completionconnect"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

func newService(t *testing.T, baseURL string, opts ...completion.Option) *completion.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return completion.NewService(sdk, opts...)
}

func newFakeService(t *testing.T, fake *rootpullertest.Completion) *completion.Service {
	t.Helper()

	return newService(t, rootpullertest.NewServer(t, fake).URL)
}

func TestComplete(t *testing.T) {
	t.Parallel()

	svc := newFakeService(t, &rootpullertest.Completion{
		CompleteFunc: func(lastContent string, _ map[string][]byte) (*completion.Response, error) {
			return &completion.Response{Content: "echo: " + lastContent, Model: "claude-fake"}, nil
		},
	})

	resp, err := svc.Complete(t.Context(),
		[]completion.Message{completion.UserMessage("hi there")},
		&completion.Options{
			Provider: completion.ProviderAnthropic,
			System:   "be brief",
		})
	if err != nil {
		t.Fatal(err)
	}

	if resp.Content != "echo: hi there" || resp.Model != "claude-fake" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestCompleteWithAttachments(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("data"), 1<<20) // 4 MiB → multiple 2 MiB chunks

	var gotAttachments map[string][]byte

	svc := newFakeService(t, &rootpullertest.Completion{
		CompleteFunc: func(_ string, attachments map[string][]byte) (*completion.Response, error) {
			gotAttachments = attachments

			return &completion.Response{Content: "done"}, nil
		},
	})

	resp, err := svc.CompleteWithAttachments(t.Context(),
		[]completion.Message{{
			Role:    completion.RoleUser,
			Content: "summarize",
			Parts: []completion.Part{
				completion.TextPart{Text: "summarize this file"},
				completion.AttachmentPart{MIMEType: "application/pdf", ID: "doc-1"},
			},
		}},
		[]completion.Attachment{{ID: "doc-1", Content: bytes.NewReader(payload)}},
		&completion.Options{Provider: completion.ProviderGemini},
	)
	if err != nil {
		t.Fatal(err)
	}

	if resp.Content != "done" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	if !bytes.Equal(gotAttachments["doc-1"], payload) {
		t.Fatalf("attachment payload mismatch: got %d bytes, want %d", len(gotAttachments["doc-1"]), len(payload))
	}
}

func TestCompleteWithEmptyAttachment(t *testing.T) {
	t.Parallel()

	var gotAttachments map[string][]byte

	svc := newFakeService(t, &rootpullertest.Completion{
		CompleteFunc: func(_ string, attachments map[string][]byte) (*completion.Response, error) {
			gotAttachments = attachments

			return &completion.Response{}, nil
		},
	})

	_, err := svc.CompleteWithAttachments(t.Context(),
		[]completion.Message{{
			Role:  completion.RoleUser,
			Parts: []completion.Part{completion.AttachmentPart{MIMEType: "text/plain", ID: "empty"}},
		}},
		[]completion.Attachment{{ID: "empty", Content: strings.NewReader("")}},
		&completion.Options{Provider: completion.ProviderOpenAI},
	)
	if err != nil {
		t.Fatal(err)
	}
	// The server requires at least one chunk per declared attachment,
	// so the empty attachment must still have arrived.
	if _, ok := gotAttachments["empty"]; !ok {
		t.Fatal("empty attachment never reached the server")
	}
}

func TestAttachmentMismatchFailsLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	// Declared but not provided.
	_, err := svc.CompleteWithAttachments(t.Context(),
		[]completion.Message{{
			Role:  completion.RoleUser,
			Parts: []completion.Part{completion.AttachmentPart{ID: "missing"}},
		}},
		nil, nil)
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("declared-but-not-provided: err = %v, want ErrInvalidArgument", err)
	}

	// Provided but not declared.
	_, err = svc.CompleteWithAttachments(t.Context(),
		[]completion.Message{completion.UserMessage("hi")},
		[]completion.Attachment{{ID: "stray", Content: strings.NewReader("x")}},
		nil)
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("provided-but-not-declared: err = %v, want ErrInvalidArgument", err)
	}
}

func TestInvalidProviderFailsLocally(t *testing.T) {
	t.Parallel()

	svc := newService(t, "http://127.0.0.1:1")

	_, err := svc.Complete(t.Context(),
		[]completion.Message{completion.UserMessage("hi")},
		&completion.Options{Provider: completion.Provider("bogus")})
	if !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

// capturingCompletion records the proto request of each Complete call.
type capturingCompletion struct {
	completionconnect.UnimplementedCompletionServiceHandler

	requests chan *completionpb.CompleteRequest
}

func (h *capturingCompletion) Complete(_ context.Context, req *connect.Request[completionpb.CompleteRequest]) (*connect.Response[completionpb.CompleteResponse], error) {
	h.requests <- req.Msg

	return connect.NewResponse(&completionpb.CompleteResponse{}), nil
}

func newCapturingServer(t *testing.T) (*rootpullertest.Server, chan *completionpb.CompleteRequest) {
	t.Helper()

	handler := &capturingCompletion{requests: make(chan *completionpb.CompleteRequest, 1)}
	mux := http.NewServeMux()
	mux.Handle(completionconnect.NewCompletionServiceHandler(handler))

	return rootpullertest.NewServerWithMux(t, mux), handler.requests
}

func TestServiceDefaults(t *testing.T) {
	t.Parallel()

	srv, requests := newCapturingServer(t)
	svc := newService(t, srv.URL,
		completion.WithDefaultProvider(completion.ProviderAnthropic),
		completion.WithDefaultModel("claude-default"))

	// The construction-time defaults apply when Options leave them
	// empty...
	if _, err := svc.Complete(t.Context(), []completion.Message{completion.UserMessage("hi")}, nil); err != nil {
		t.Fatal(err)
	}

	msg := <-requests
	if msg.GetProvider() != completionpb.Provider_PROVIDER_ANTHROPIC {
		t.Errorf("provider = %v, want service default anthropic", msg.GetProvider())
	}

	if msg.Model == nil || msg.GetModel() != "claude-default" {
		t.Errorf("model = %v, want service default", msg.Model)
	}
	// ...unset optionals stay off the wire...
	if msg.System != nil || msg.ResponseSchema != nil || msg.Temperature != nil || msg.MaxTokens != nil {
		t.Errorf("unexpected optional fields set: %+v", msg)
	}

	// ...and per-call values win over the defaults.
	temperature := float32(0)

	_, err := svc.Complete(t.Context(), []completion.Message{completion.UserMessage("hi")}, &completion.Options{
		Provider:       completion.ProviderOllama,
		Model:          "llama3",
		System:         "be brief",
		ResponseSchema: `{"type":"object"}`,
		Temperature:    &temperature,
		MaxTokens:      64,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg = <-requests
	if msg.GetProvider() != completionpb.Provider_PROVIDER_OLLAMA || msg.GetModel() != "llama3" {
		t.Errorf("provider/model = %v/%q, want per-call override", msg.GetProvider(), msg.GetModel())
	}

	if msg.GetSystem() != "be brief" || msg.GetResponseSchema() != `{"type":"object"}` {
		t.Errorf("system/schema = %q/%q, want per-call values", msg.GetSystem(), msg.GetResponseSchema())
	}
	// Temperature 0 is meaningful and must survive as an explicit 0.
	if msg.Temperature == nil || msg.GetTemperature() != 0 {
		t.Errorf("temperature = %v, want explicit 0", msg.Temperature)
	}

	if msg.MaxTokens == nil || msg.GetMaxTokens() != 64 {
		t.Errorf("maxTokens = %v, want 64", msg.MaxTokens)
	}
}

func TestJSON(t *testing.T) {
	t.Parallel()

	type verdict struct {
		Answer     string  `json:"answer"`
		Confidence float64 `json:"confidence"`
	}

	svc := newFakeService(t, &rootpullertest.Completion{
		CompleteFunc: func(string, map[string][]byte) (*completion.Response, error) {
			return &completion.Response{Content: `{"answer":"yes","confidence":0.9}`, Model: "claude-fake"}, nil
		},
	})

	got, resp, err := completion.JSON[verdict](t.Context(), svc,
		[]completion.Message{completion.UserMessage("is water wet?")},
		&completion.Options{ResponseSchema: `{"type":"object"}`})
	if err != nil {
		t.Fatal(err)
	}

	if got.Answer != "yes" || got.Confidence != 0.9 {
		t.Errorf("unmarshaled value = %+v", got)
	}

	if resp == nil || resp.Model != "claude-fake" {
		t.Errorf("response = %+v, want the raw completion response", resp)
	}
}

func TestJSONUnmarshalFailure(t *testing.T) {
	t.Parallel()

	type verdict struct {
		Answer string `json:"answer"`
	}

	svc := newFakeService(t, &rootpullertest.Completion{
		CompleteFunc: func(string, map[string][]byte) (*completion.Response, error) {
			return &completion.Response{Content: "sorry, I cannot answer in JSON"}, nil
		},
	})

	_, resp, err := completion.JSON[verdict](t.Context(), svc,
		[]completion.Message{completion.UserMessage("q")}, nil)
	if err == nil {
		t.Fatal("want an unmarshal error")
	}

	if !strings.Contains(err.Error(), "unmarshaling completion content") {
		t.Errorf("err = %v, want it to describe the unmarshal failure", err)
	}
	// The raw response still comes back so callers can inspect the
	// content that failed to parse.
	if resp == nil || resp.Content == "" {
		t.Errorf("response = %+v, want the raw completion response", resp)
	}
}
