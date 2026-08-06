package completion_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	rootpuller "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/completion"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

func newClient(t *testing.T, fake *rootpullertest.Completion) *rootpuller.Client {
	t.Helper()
	srv := rootpullertest.NewServer(t, fake)

	c, err := rootpuller.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestComplete(t *testing.T) {
	t.Parallel()

	c := newClient(t, &rootpullertest.Completion{
		CompleteFunc: func(lastContent string, _ map[string][]byte) (*completion.Response, error) {
			return &completion.Response{Content: "echo: " + lastContent, Model: "claude-fake"}, nil
		},
	})

	resp, err := c.Completion().Complete(t.Context(), &completion.Request{
		Provider: completion.ProviderAnthropic,
		Messages: []completion.Message{completion.UserMessage("hi there")},
		System:   new("be brief"),
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

	c := newClient(t, &rootpullertest.Completion{
		CompleteFunc: func(_ string, attachments map[string][]byte) (*completion.Response, error) {
			gotAttachments = attachments

			return &completion.Response{Content: "done"}, nil
		},
	})

	resp, err := c.Completion().CompleteWithAttachments(t.Context(),
		&completion.Request{
			Provider: completion.ProviderGemini,
			Messages: []completion.Message{{
				Role:    completion.RoleUser,
				Content: "summarize",
				Parts: []completion.Part{
					completion.TextPart{Text: "summarize this file"},
					completion.AttachmentPart{MIMEType: "application/pdf", ID: "doc-1"},
				},
			}},
		},
		completion.Attachment{ID: "doc-1", Content: bytes.NewReader(payload)},
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

	c := newClient(t, &rootpullertest.Completion{
		CompleteFunc: func(_ string, attachments map[string][]byte) (*completion.Response, error) {
			gotAttachments = attachments

			return &completion.Response{}, nil
		},
	})

	_, err := c.Completion().CompleteWithAttachments(t.Context(),
		&completion.Request{
			Provider: completion.ProviderOpenAI,
			Messages: []completion.Message{{
				Role:  completion.RoleUser,
				Parts: []completion.Part{completion.AttachmentPart{MIMEType: "text/plain", ID: "empty"}},
			}},
		},
		completion.Attachment{ID: "empty", Content: strings.NewReader("")},
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

	c, err := rootpuller.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	// Declared but not provided.
	_, err = c.Completion().CompleteWithAttachments(t.Context(), &completion.Request{
		Provider: completion.ProviderAnthropic,
		Messages: []completion.Message{{
			Role:  completion.RoleUser,
			Parts: []completion.Part{completion.AttachmentPart{ID: "missing"}},
		}},
	})
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("declared-but-not-provided: err = %v, want ErrInvalidArgument", err)
	}

	// Provided but not declared.
	_, err = c.Completion().CompleteWithAttachments(t.Context(),
		&completion.Request{
			Provider: completion.ProviderAnthropic,
			Messages: []completion.Message{completion.UserMessage("hi")},
		},
		completion.Attachment{ID: "stray", Content: strings.NewReader("x")},
	)
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("provided-but-not-declared: err = %v, want ErrInvalidArgument", err)
	}
}

func TestInvalidProviderFailsLocally(t *testing.T) {
	t.Parallel()

	c, err := rootpuller.New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Completion().Complete(t.Context(), &completion.Request{
		Provider: completion.Provider("bogus"),
		Messages: []completion.Message{completion.UserMessage("hi")},
	})
	if !errors.Is(err, apierror.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}
