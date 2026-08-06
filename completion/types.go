package completion

import (
	"fmt"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	"github.com/entwico/rootpuller-sdk/common"
	completionpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/completion"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// Provider selects the LLM backend.
type Provider string

const (
	ProviderDefault   Provider = ""
	ProviderAnthropic Provider = "anthropic"
	ProviderGemini    Provider = "gemini"
	ProviderOpenAI    Provider = "openai"
	ProviderOllama    Provider = "ollama"
	ProviderLiteLLM   Provider = "litellm"
)

var providerToProto = map[Provider]completionpb.Provider{
	ProviderDefault:   completionpb.Provider_PROVIDER_UNSPECIFIED,
	ProviderAnthropic: completionpb.Provider_PROVIDER_ANTHROPIC,
	ProviderGemini:    completionpb.Provider_PROVIDER_GEMINI,
	ProviderOpenAI:    completionpb.Provider_PROVIDER_OPENAI,
	ProviderOllama:    completionpb.Provider_PROVIDER_OLLAMA,
	ProviderLiteLLM:   completionpb.Provider_PROVIDER_LITELLM,
}

// ThinkingEffort controls extended-thinking depth for providers that
// support it. The zero value keeps the server default.
type ThinkingEffort string

const (
	ThinkingEffortDefault ThinkingEffort = ""
	ThinkingEffortOff     ThinkingEffort = "off"
	ThinkingEffortLow     ThinkingEffort = "low"
	ThinkingEffortMedium  ThinkingEffort = "medium"
	ThinkingEffortHigh    ThinkingEffort = "high"
)

var thinkingEffortToProto = map[ThinkingEffort]completionpb.ThinkingEffort{
	ThinkingEffortDefault: completionpb.ThinkingEffort_THINKING_EFFORT_UNSPECIFIED,
	ThinkingEffortOff:     completionpb.ThinkingEffort_THINKING_EFFORT_OFF,
	ThinkingEffortLow:     completionpb.ThinkingEffort_THINKING_EFFORT_LOW,
	ThinkingEffortMedium:  completionpb.ThinkingEffort_THINKING_EFFORT_MEDIUM,
	ThinkingEffortHigh:    completionpb.ThinkingEffort_THINKING_EFFORT_HIGH,
}

// Role identifies the author of a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

var roleToProto = map[Role]completionpb.Message_Role{
	RoleUser:      completionpb.Message_ROLE_USER,
	RoleAssistant: completionpb.Message_ROLE_ASSISTANT,
}

func invalidArgument(msg string) error {
	return apierror.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// Part is one content block of a multimodal Message. Implementations:
// TextPart, FilePart, AttachmentPart.
type Part interface{ isPart() }

// TextPart is a plain text block.
type TextPart struct {
	Text string
}

func (TextPart) isPart() {}

// FilePart is a file carried inline in the request. Complete accepts it;
// for payloads beyond a few megabytes use AttachmentPart with
// CompleteWithAttachments instead.
type FilePart struct {
	MIMEType string
	Name     string // optional display name
	Data     []byte
}

func (FilePart) isPart() {}

// AttachmentPart references a file streamed separately via
// CompleteWithAttachments. ID must match one Attachment's ID. Complete
// (the unary RPC) rejects attachment parts.
type AttachmentPart struct {
	MIMEType string
	Name     string // optional display name
	ID       string
}

func (AttachmentPart) isPart() {}

// Message is one conversation turn. When Parts is non-empty it wins over
// Content (the server rule); use Content alone for plain text.
type Message struct {
	Role    Role
	Content string
	Parts   []Part
}

// UserMessage builds a plain-text user turn.
func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

// AssistantMessage builds a plain-text assistant turn.
func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: content}
}

func (m Message) toProto() (*completionpb.Message, error) {
	role, ok := roleToProto[m.Role]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown message role %q", m.Role))
	}

	msg := &completionpb.Message{Role: role, Content: m.Content}
	for _, part := range m.Parts {
		switch p := part.(type) {
		case TextPart:
			msg.Parts = append(msg.Parts, &completionpb.Part{Part: &completionpb.Part_Text{Text: p.Text}})
		case FilePart:
			file := &completionpb.Part_File{MimeType: p.MIMEType, Source: &completionpb.Part_File_Data{Data: p.Data}}
			if p.Name != "" {
				file.Name = &p.Name
			}

			msg.Parts = append(msg.Parts, &completionpb.Part{Part: &completionpb.Part_File_{File: file}})
		case AttachmentPart:
			if p.ID == "" {
				return nil, invalidArgument("attachment part with empty ID")
			}

			file := &completionpb.Part_File{MimeType: p.MIMEType, Source: &completionpb.Part_File_AttachmentId{AttachmentId: p.ID}}
			if p.Name != "" {
				file.Name = &p.Name
			}

			msg.Parts = append(msg.Parts, &completionpb.Part{Part: &completionpb.Part_File_{File: file}})
		default:
			return nil, invalidArgument(fmt.Sprintf("unknown message part type %T", part))
		}
	}

	return msg, nil
}

// Request configures Complete and CompleteWithAttachments.
type Request struct {
	Provider Provider
	Messages []Message
	// Model overrides the provider's default model.
	Model *string
	// System is the system prompt.
	System *string
	// ResponseSchema is a JSON schema enforcing structured output.
	ResponseSchema *string
	Temperature    *float32
	MaxTokens      *int
	ThinkingEffort ThinkingEffort
	// ThinkingBudgetTokens caps extended thinking for providers that
	// meter it.
	ThinkingBudgetTokens *int
}

func (r *Request) toProto() (*completionpb.CompleteRequest, error) {
	provider, ok := providerToProto[r.Provider]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown provider %q", r.Provider))
	}

	effort, ok := thinkingEffortToProto[r.ThinkingEffort]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown thinking effort %q", r.ThinkingEffort))
	}

	msg := &completionpb.CompleteRequest{
		Provider:             provider,
		Model:                r.Model,
		System:               r.System,
		ResponseSchema:       r.ResponseSchema,
		Temperature:          r.Temperature,
		MaxTokens:            protoconv.Int32Ptr(r.MaxTokens),
		ThinkingBudgetTokens: protoconv.Int32Ptr(r.ThinkingBudgetTokens),
	}
	if effort != completionpb.ThinkingEffort_THINKING_EFFORT_UNSPECIFIED {
		msg.ThinkingEffort = &effort
	}

	for _, m := range r.Messages {
		pm, err := m.toProto()
		if err != nil {
			return nil, err
		}

		msg.Messages = append(msg.Messages, pm)
	}

	return msg, nil
}

// Response is the completion result.
type Response struct {
	Content string
	// Model is the concrete model that served the request.
	Model string
	Usage common.Usage
	// Thinking is the extended-thinking transcript when requested.
	Thinking string
}
