// Package vectorops wraps
// com.entwico.rootpuller.vectorops.VectorOpsService: HDBSCAN clustering
// and UMAP projection over dense float32 matrices, streamed in chunks
// because a single embedding set can exceed the per-message limit.
package vectorops

import (
	"context"
	"fmt"
	"iter"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/apierror"
	vectoropspb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/vectorops"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/vectorops/vectoropsconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Client calls the VectorOpsService. Obtain one from
// rootpuller.Client.VectorOps.
type Client struct {
	rpc        vectoropsconnect.VectorOpsServiceClient
	deployment string
}

// NewFromCore is the internal constructor used by rootpuller.New.
func NewFromCore(core *transport.Core) *Client {
	return &Client{rpc: vectoropsconnect.NewVectorOpsServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// WithDeployment returns a client that sends the rootpuller-deployment
// routing header (e.g. "local", "cloudrun") on every call. A per-call
// rootpuller.ContextWithDeployment value still wins.
func (c *Client) WithDeployment(name string) *Client {
	derived := *c
	derived.deployment = name
	return &derived
}

// Outbound chunk sizing. Frames must stay under streamio.MaxChunkBytes
// (2 MiB) on the wire; 400k float32s (~1.6 MB) and 40k ids leave headroom
// for framing overhead. Data chunks need not align to row boundaries and
// ids accumulate independently server-side, so ids travel in their own
// frames after the data.
const (
	maxFloatsPerFrame = 400_000
	maxIDsPerFrame    = 40_000
	maxLabelsPerFrame = 262_144
)

// matrixFrames slices a matrix into MatrixChunk frames wrapped into the
// service's request type: data-only frames first, then ids-only frames.
func matrixFrames[Req any](m *Matrix, wrap func(*vectoropspb.MatrixChunk) *Req) iter.Seq2[*Req, error] {
	return func(yield func(*Req, error) bool) {
		for data := m.Data; len(data) > 0; {
			n := min(len(data), maxFloatsPerFrame)
			if !yield(wrap(&vectoropspb.MatrixChunk{Data: data[:n:n]}), nil) {
				return
			}
			data = data[n:]
		}
		for ids := m.IDs; len(ids) > 0; {
			n := min(len(ids), maxIDsPerFrame)
			if !yield(wrap(&vectoropspb.MatrixChunk{Ids: ids[:n:n]}), nil) {
				return
			}
			ids = ids[n:]
		}
	}
}

// labelFrames slices supervised labels into SupervisedLabelsChunk frames.
func labelFrames(labels []int32) iter.Seq2[*vectoropspb.ProjectUmapRequest, error] {
	return func(yield func(*vectoropspb.ProjectUmapRequest, error) bool) {
		for len(labels) > 0 {
			n := min(len(labels), maxLabelsPerFrame)
			frame := &vectoropspb.ProjectUmapRequest{
				Request: &vectoropspb.ProjectUmapRequest_SupervisedLabels{
					SupervisedLabels: &vectoropspb.SupervisedLabelsChunk{Labels: labels[:n:n]},
				},
			}
			if !yield(frame, nil) {
				return
			}
			labels = labels[n:]
		}
	}
}

// ClusterHdbscan calls VectorOpsService/ClusterHdbscan: groups points into
// clusters with HDBSCAN, labelling low-density points as noise. On the
// wire it sends exactly one header frame, then the points matrix as
// chunks, half-closes, and collects one metadata frame followed by
// row-aligned result chunks. A nil params keeps all server defaults.
func (c *Client) ClusterHdbscan(ctx context.Context, points Matrix, params *HdbscanParams) (*HdbscanResult, error) {
	ctx = transport.EnsureDeployment(ctx, c.deployment)
	if err := points.validate(); err != nil {
		return nil, err
	}
	header, err := params.toHeader(&points)
	if err != nil {
		return nil, err
	}
	procedure := vectoropsconnect.VectorOpsServiceClusterHdbscanProcedure
	stream := c.rpc.ClusterHdbscan(ctx)

	frames := streamio.Frames(
		&vectoropspb.ClusterHdbscanRequest{Request: &vectoropspb.ClusterHdbscanRequest_Header_{Header: header}},
		matrixFrames(&points, func(chunk *vectoropspb.MatrixChunk) *vectoropspb.ClusterHdbscanRequest {
			return &vectoropspb.ClusterHdbscanRequest{Request: &vectoropspb.ClusterHdbscanRequest_Points{Points: chunk}}
		}),
	)

	var result HdbscanResult
	var gotMetadata bool
	err = streamio.UploadCollect(stream, procedure, frames, func(resp *vectoropspb.ClusterHdbscanResponse) error {
		switch r := resp.GetResponse().(type) {
		case *vectoropspb.ClusterHdbscanResponse_Metadata_:
			gotMetadata = true
			result.NumClusters = int(r.Metadata.GetNumClusters())
		case *vectoropspb.ClusterHdbscanResponse_Chunk:
			result.Labels = append(result.Labels, r.Chunk.GetLabels()...)
			result.Probabilities = append(result.Probabilities, r.Chunk.GetProbabilities()...)
			result.OutlierScores = append(result.OutlierScores, r.Chunk.GetOutlierScores()...)
			result.IDs = append(result.IDs, r.Chunk.GetIds()...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !gotMetadata {
		return nil, apierror.New(connect.CodeInternal, "server sent no metadata frame", procedure, 0, nil)
	}
	if len(result.Labels) != points.Rows {
		return nil, apierror.New(connect.CodeInternal,
			fmt.Sprintf("server streamed %d labels, want %d (one per input row)", len(result.Labels), points.Rows),
			procedure, 0, nil)
	}
	return &result, nil
}

// ProjectUmap calls VectorOpsService/ProjectUmap: reduces points to a
// lower-dimensional embedding via UMAP. On the wire it sends exactly one
// header frame, then the points matrix as chunks (plus supervised-labels
// chunks when params.SupervisedLabels is set), half-closes, and collects
// one metadata frame (output dimensions) followed by row-aligned embedding
// chunks. A nil params keeps all server defaults.
func (c *Client) ProjectUmap(ctx context.Context, points Matrix, params *UmapParams) (*UmapResult, error) {
	ctx = transport.EnsureDeployment(ctx, c.deployment)
	if err := points.validate(); err != nil {
		return nil, err
	}
	var labels []int32
	if params != nil {
		labels = params.SupervisedLabels
	}
	if len(labels) != 0 && len(labels) != points.Rows {
		return nil, invalidArgument(fmt.Sprintf("got %d supervised labels for %d rows; labels must be empty or one per row", len(labels), points.Rows))
	}
	header, err := params.toHeader(&points)
	if err != nil {
		return nil, err
	}
	procedure := vectoropsconnect.VectorOpsServiceProjectUmapProcedure
	stream := c.rpc.ProjectUmap(ctx)

	frames := streamio.Frames(
		&vectoropspb.ProjectUmapRequest{Request: &vectoropspb.ProjectUmapRequest_Header_{Header: header}},
		matrixFrames(&points, func(chunk *vectoropspb.MatrixChunk) *vectoropspb.ProjectUmapRequest {
			return &vectoropspb.ProjectUmapRequest{Request: &vectoropspb.ProjectUmapRequest_Points{Points: chunk}}
		}),
		labelFrames(labels),
	)

	var result UmapResult
	var gotMetadata bool
	err = streamio.UploadCollect(stream, procedure, frames, func(resp *vectoropspb.ProjectUmapResponse) error {
		switch r := resp.GetResponse().(type) {
		case *vectoropspb.ProjectUmapResponse_Metadata_:
			gotMetadata = true
			result.Embedding.Rows = int(r.Metadata.GetRows())
			result.Embedding.Cols = int(r.Metadata.GetCols())
		case *vectoropspb.ProjectUmapResponse_Embedding:
			result.Embedding.Data = append(result.Embedding.Data, r.Embedding.GetData()...)
			result.Embedding.IDs = append(result.Embedding.IDs, r.Embedding.GetIds()...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !gotMetadata {
		return nil, apierror.New(connect.CodeInternal, "server sent no metadata frame", procedure, 0, nil)
	}
	if want := result.Embedding.Rows * result.Embedding.Cols; len(result.Embedding.Data) != want {
		return nil, apierror.New(connect.CodeInternal,
			fmt.Sprintf("server streamed %d embedding values, want %d (declared %d rows x %d cols)",
				len(result.Embedding.Data), want, result.Embedding.Rows, result.Embedding.Cols),
			procedure, 0, nil)
	}
	return &result, nil
}
