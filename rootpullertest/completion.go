package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/completion"
	completionpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/completion"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/completion/completionconnect"
)

// Completion is a facade-typed fake CompletionService. CompleteFunc
// receives the last user message content and, for uploads, the streamed
// attachment payloads keyed by attachment ID. A nil CompleteFunc echoes
// the last message content.
type Completion struct {
	CompleteFunc func(lastContent string, attachments map[string][]byte) (*completion.Response, error)
}

func (f *Completion) register(mux *http.ServeMux) {
	mux.Handle(completionconnect.NewCompletionServiceHandler(&completionHandler{fake: f}))
}

type completionHandler struct {
	completionconnect.UnimplementedCompletionServiceHandler

	fake *Completion
}

func (h *completionHandler) respond(req *completionpb.CompleteRequest, attachments map[string][]byte) (*completionpb.CompleteResponse, error) {
	var lastContent string
	if msgs := req.GetMessages(); len(msgs) > 0 {
		lastContent = msgs[len(msgs)-1].GetContent()
	}
	completeFunc := h.fake.CompleteFunc
	if completeFunc == nil {
		completeFunc = func(lastContent string, _ map[string][]byte) (*completion.Response, error) {
			return &completion.Response{Content: lastContent, Model: "fake-model"}, nil
		}
	}
	resp, err := completeFunc(lastContent, attachments)
	if err != nil {
		return nil, err
	}
	return &completionpb.CompleteResponse{
		Content:  resp.Content,
		Model:    resp.Model,
		Thinking: resp.Thinking,
	}, nil
}

func (h *completionHandler) Complete(_ context.Context, req *connect.Request[completionpb.CompleteRequest]) (*connect.Response[completionpb.CompleteResponse], error) {
	resp, err := h.respond(req.Msg, nil)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (h *completionHandler) CompleteUpload(_ context.Context, stream *connect.ClientStream[completionpb.CompleteUploadRequest]) (*connect.Response[completionpb.CompleteUploadResponse], error) {
	var request *completionpb.CompleteRequest
	attachments := map[string][]byte{}
	for stream.Receive() {
		switch frame := stream.Msg().GetFrame().(type) {
		case *completionpb.CompleteUploadRequest_Request:
			if request != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only the first frame may be a request"))
			}
			request = frame.Request
		case *completionpb.CompleteUploadRequest_Chunk:
			if request == nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("first frame must be a request"))
			}
			id := frame.Chunk.GetAttachmentId()
			if _, ok := attachments[id]; !ok {
				attachments[id] = []byte{}
			}
			attachments[id] = append(attachments[id], frame.Chunk.GetData()...)
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if request == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("stream closed before request frame"))
	}
	resp, err := h.respond(request, attachments)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&completionpb.CompleteUploadResponse{Response: resp}), nil
}
