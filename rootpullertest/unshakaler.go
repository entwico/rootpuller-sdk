package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/common"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	unshakalerpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/unshakaler"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/unshakaler/unshakalerconnect"
)

// Unshakaler is a facade-typed fake UnshakalerService. It enforces the
// wire protocol strictly (params frame first, chunks ≤2 MiB, no work
// before the client half-closes) so SDK regressions surface as test
// failures. UpscaleFunc receives the model name and the reassembled
// input; a nil UpscaleFunc echoes the input with an "upscaled-" name
// prefix. Progress values are streamed before the result.
type Unshakaler struct {
	UpscaleFunc func(model string, image common.File) (*common.File, error)
	Progress    []float32
}

func (f *Unshakaler) register(mux *http.ServeMux) {
	mux.Handle(unshakalerconnect.NewUnshakalerServiceHandler(&unshakalerHandler{fake: f}))
}

type unshakalerHandler struct {
	fake *Unshakaler
}

func (h *unshakalerHandler) UpscaleImage(_ context.Context, stream *connect.BidiStream[unshakalerpb.UpscaleImageRequest, unshakalerpb.UpscaleImageResponse]) error {
	var (
		params *unshakalerpb.UpscaleImageRequest_Params
		input  common.File
	)
	// Like the real server: drain the full request before any work.

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		switch r := req.GetRequest().(type) {
		case *unshakalerpb.UpscaleImageRequest_Params_:
			if params != nil {
				return invalidArgument(errDuplicateParamsFrame)
			}

			params = r.Params
		case *unshakalerpb.UpscaleImageRequest_File:
			if params == nil {
				return errBeforeParams("file")
			}

			if err := appendChunk(&input, r.File); err != nil {
				return err
			}
		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if params == nil {
		return invalidArgument(errMissingParams)
	}

	upscale := h.fake.UpscaleFunc
	if upscale == nil {
		upscale = func(_ string, image common.File) (*common.File, error) {
			return &common.File{Name: "upscaled-" + image.Name, MIMEType: image.MIMEType, Data: image.Data}, nil
		}
	}

	result, err := upscale(params.GetModelName(), input)
	if err != nil {
		return err
	}

	for _, p := range h.fake.Progress {
		if err := stream.Send(&unshakalerpb.UpscaleImageResponse{
			Response: &unshakalerpb.UpscaleImageResponse_ProgressPercentage{ProgressPercentage: p},
		}); err != nil {
			return err
		}
	}

	return SendFileChunks(result, func(chunk *commonpb.FileChunk) error {
		return stream.Send(&unshakalerpb.UpscaleImageResponse{
			Response: &unshakalerpb.UpscaleImageResponse_File{File: chunk},
		})
	})
}

// SendFileChunks streams a file back to the client the way the real
// server does: 2 MiB chunks, each repeating name, MIME type, and the
// total content length. Exported for use by hand-built test handlers.
func SendFileChunks(f *common.File, send func(*commonpb.FileChunk) error) error {
	const chunkSize = 2 << 20

	data := f.Data

	total := int32(len(data)) //nolint:gosec // test-fixture file sizes fit int32
	for first := true; first || len(data) > 0; first = false {
		n := min(len(data), chunkSize)

		chunk := &commonpb.FileChunk{
			Name:          f.Name,
			MimeType:      f.MIMEType,
			ContentLength: total,
			Data:          data[:n],
		}
		if err := send(chunk); err != nil {
			return err
		}

		data = data[n:]
	}

	return nil
}
