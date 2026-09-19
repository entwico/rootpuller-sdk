package escriba

import (
	"context"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	escribapb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/escriba/escribaconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// TranscribeRecording transcribes a complete recording of any length, with
// optional speaker labels.
//
// The call is the job: it returns when the recording is done, and cancelling
// ctx cancels the work on the server. There is no job id and nothing to poll.
// A recording can take minutes and may first wait behind another one, so give
// ctx a deadline that allows for it — or none — and use opts.OnProgress to see
// where it is.
//
// Segments are final as they arrive. Without speaker labels opts.OnSegment is
// called while the recording is still being transcribed; with them, all
// segments arrive together at the end, because a segment ends where the speaker
// changes and that is only known once the whole recording has been heard.
// Either way the returned Recording holds all of them.
//
// Use Transcribe instead for clips under Capabilities.MaxRecording: it is a
// single round trip.
func (s *Service) TranscribeRecording(
	ctx context.Context,
	audio rootpullersdk.Upload,
	opts *RecordingOptions,
) (*Recording, error) {
	if opts == nil {
		opts = &RecordingOptions{}
	}

	cfg, err := opts.toProto(s)
	if err != nil {
		return nil, err
	}

	ctx = transport.EnsureEscribaDeployment(ctx, s.deployment)
	procedure := escribaconnect.TranscriptionServiceTranscribeRecordingProcedure
	stream := s.rpc.TranscribeRecording(ctx)

	frames := streamio.Frames(
		&escribapb.TranscribeRecordingRequest{
			Frame: &escribapb.TranscribeRecordingRequest_Config{Config: cfg},
		},
		streamio.FileChunkFrames(audio, func(chunk *commonpb.FileChunk) *escribapb.TranscribeRecordingRequest {
			return &escribapb.TranscribeRecordingRequest{
				Frame: &escribapb.TranscribeRecordingRequest_Chunk{Chunk: chunk},
			}
		}),
	)

	var (
		recording Recording
		complete  bool
	)

	err = streamio.UploadCollect(stream, procedure, frames, func(resp *escribapb.TranscribeRecordingResponse) error {
		switch event := resp.GetEvent().(type) {
		case *escribapb.TranscribeRecordingResponse_Progress:
			if opts.OnProgress != nil {
				opts.OnProgress(recordingProgressFromProto(event.Progress))
			}

		case *escribapb.TranscribeRecordingResponse_Segment:
			segment := segmentFromProto(event.Segment)
			recording.Segments = append(recording.Segments, segment)

			if opts.OnSegment != nil {
				opts.OnSegment(segment)
			}

		case *escribapb.TranscribeRecordingResponse_Complete:
			recording.applyComplete(event.Complete)

			complete = true
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// A stream that ends cleanly without its final event is a truncated
	// transcript, and must not be handed over as if it were a whole one.
	if !complete {
		return nil, apierr.New(connect.CodeInternal,
			"server ended the recording without a complete event", procedure, 0, nil)
	}

	if got, want := len(recording.Segments), recording.segmentCount; got != want {
		return nil, apierr.New(connect.CodeDataLoss,
			"server reported a different number of segments than it sent", procedure, 0, nil)
	}

	return &recording, nil
}
