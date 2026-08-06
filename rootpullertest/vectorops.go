package rootpullertest

import (
	"context"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	vectoropspb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/vectorops"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/vectorops/vectoropsconnect"
	"github.com/entwico/rootpuller-sdk/vectorops"
)

// VectorOps is a facade-typed fake VectorOpsService. It enforces the wire
// protocol strictly (exactly one header first, data and ids accumulated
// independently, no work before the client half-closes, matrix dimensions
// validated at EOF) so SDK regressions surface as test failures.
// ClusterFunc / UmapFunc receive the reassembled points matrix; nil hooks
// serve canned results (one all-points cluster; a 2D projection of the
// first two input columns). Results are streamed back in row-aligned
// chunks of 1000 rows to exercise multi-chunk reassembly.
type VectorOps struct {
	ClusterFunc func(points vectorops.Matrix, opts *vectorops.HdbscanOptions) (*vectorops.HdbscanResult, error)
	UmapFunc    func(points vectorops.Matrix, labels []int32) (*vectorops.UmapResult, error)
}

func (f *VectorOps) register(mux *http.ServeMux) {
	mux.Handle(vectoropsconnect.NewVectorOpsServiceHandler(&vectorOpsHandler{fake: f}))
}

type vectorOpsHandler struct {
	fake *VectorOps
}

// vectorOpsRowsPerChunk is the row-aligned response chunk size, small
// enough that realistic tests span several chunks.
const vectorOpsRowsPerChunk = 1000

// VectorOps-specific protocol-violation sentinels.
var (
	errClusterResultMismatch = errors.New("clustering result arrays disagree with input row count")
	errUmapResultMismatch    = errors.New("umap embedding dimensions disagree with input row count")
	errMatrixDims            = errors.New("points matrix must have rows > 0 and cols > 0")
	errMatrixDataLength      = errors.New("points data length must equal rows * cols")
	errMatrixIDsLength       = errors.New("points ids length must equal rows when set")
	errLabelsLength          = errors.New("supervised_labels length must equal rows when set")
)

func (h *vectorOpsHandler) ClusterHdbscan(_ context.Context, stream *connect.BidiStream[vectoropspb.ClusterHdbscanRequest, vectoropspb.ClusterHdbscanResponse]) error {
	var (
		header *vectoropspb.ClusterHdbscanRequest_Header
		data   []float32
		ids    []string
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
		case *vectoropspb.ClusterHdbscanRequest_Header_:
			if header != nil {
				return invalidArgument(errDuplicateHeaderFrame)
			}

			header = r.Header
		case *vectoropspb.ClusterHdbscanRequest_Points:
			if header == nil {
				return errBeforeHeader("points")
			}

			data = append(data, r.Points.GetData()...)
			ids = append(ids, r.Points.GetIds()...)
		default:
			return invalidArgument(errUnexpectedVariant)
		}
	}

	if header == nil {
		return invalidArgument(errMissingHeader)
	}

	points, err := buildFacadeMatrix(header.GetRows(), header.GetCols(), data, ids)
	if err != nil {
		return err
	}

	cluster := h.fake.ClusterFunc
	if cluster == nil {
		cluster = func(points vectorops.Matrix, _ *vectorops.HdbscanOptions) (*vectorops.HdbscanResult, error) {
			result := &vectorops.HdbscanResult{
				NumClusters:   1,
				Labels:        make([]int32, points.Rows),
				Probabilities: make([]float32, points.Rows),
				OutlierScores: make([]float32, points.Rows),
				IDs:           points.IDs,
			}
			for i := range result.Probabilities {
				result.Probabilities[i] = 1
			}

			return result, nil
		}
	}

	result, err := cluster(points, hdbscanOptionsFromProto(header))
	if err != nil {
		return err
	}

	rows := len(result.Labels)
	if rows != points.Rows || len(result.Probabilities) != rows || len(result.OutlierScores) != rows ||
		(len(result.IDs) != 0 && len(result.IDs) != rows) {
		return connect.NewError(connect.CodeInternal, errClusterResultMismatch)
	}

	if err := stream.Send(&vectoropspb.ClusterHdbscanResponse{
		Response: &vectoropspb.ClusterHdbscanResponse_Metadata_{
			Metadata: &vectoropspb.ClusterHdbscanResponse_Metadata{NumClusters: int32(result.NumClusters)}, //nolint:gosec // test-fixture counts fit int32
		},
	}); err != nil {
		return err
	}

	for offset := 0; offset < rows; offset += vectorOpsRowsPerChunk {
		end := min(offset+vectorOpsRowsPerChunk, rows)

		chunk := &vectoropspb.ClusterHdbscanResponse_ResultChunk{
			Labels:        result.Labels[offset:end],
			Probabilities: result.Probabilities[offset:end],
			OutlierScores: result.OutlierScores[offset:end],
		}
		if len(result.IDs) != 0 {
			chunk.Ids = result.IDs[offset:end]
		}

		if err := stream.Send(&vectoropspb.ClusterHdbscanResponse{
			Response: &vectoropspb.ClusterHdbscanResponse_Chunk{Chunk: chunk},
		}); err != nil {
			return err
		}
	}

	return nil
}

// umapRequest holds the frames accumulated from one ProjectUmap request
// stream.
type umapRequest struct {
	header *vectoropspb.ProjectUmapRequest_Header
	data   []float32
	ids    []string
	labels []int32
}

// receiveProjectUmapRequest drains a ProjectUmap request stream, enforcing
// the header-first frame ordering, and returns the accumulated frames.
func receiveProjectUmapRequest(stream *connect.BidiStream[vectoropspb.ProjectUmapRequest, vectoropspb.ProjectUmapResponse]) (*umapRequest, error) {
	acc := &umapRequest{}

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return acc, nil
			}

			return nil, err
		}

		switch r := req.GetRequest().(type) {
		case *vectoropspb.ProjectUmapRequest_Header_:
			if acc.header != nil {
				return nil, invalidArgument(errDuplicateHeaderFrame)
			}

			acc.header = r.Header
		case *vectoropspb.ProjectUmapRequest_Points:
			if acc.header == nil {
				return nil, errBeforeHeader("points")
			}

			acc.data = append(acc.data, r.Points.GetData()...)
			acc.ids = append(acc.ids, r.Points.GetIds()...)
		case *vectoropspb.ProjectUmapRequest_SupervisedLabels:
			if acc.header == nil {
				return nil, errBeforeHeader("supervised_labels")
			}

			acc.labels = append(acc.labels, r.SupervisedLabels.GetLabels()...)
		default:
			return nil, invalidArgument(errUnexpectedVariant)
		}
	}
}

func (h *vectorOpsHandler) ProjectUmap(_ context.Context, stream *connect.BidiStream[vectoropspb.ProjectUmapRequest, vectoropspb.ProjectUmapResponse]) error {
	// Like the real server: drain the full request before any work.
	frames, err := receiveProjectUmapRequest(stream)
	if err != nil {
		return err
	}

	if frames.header == nil {
		return invalidArgument(errMissingHeader)
	}

	points, err := buildFacadeMatrix(frames.header.GetRows(), frames.header.GetCols(), frames.data, frames.ids)
	if err != nil {
		return err
	}

	labels := frames.labels
	if len(labels) != 0 && len(labels) != points.Rows {
		return invalidArgument(errLabelsLength)
	}

	umap := h.fake.UmapFunc
	if umap == nil {
		umap = func(points vectorops.Matrix, _ []int32) (*vectorops.UmapResult, error) {
			// Identity-ish 2D projection: the first two input columns
			// (zero-padded when the input has fewer).
			out := vectorops.Matrix{Rows: points.Rows, Cols: 2, IDs: points.IDs}

			out.Data = make([]float32, out.Rows*out.Cols)
			for i := range points.Rows {
				for j := range min(points.Cols, 2) {
					out.Data[i*2+j] = points.Data[i*points.Cols+j]
				}
			}

			return &vectorops.UmapResult{Embedding: out}, nil
		}
	}

	result, err := umap(points, labels)
	if err != nil {
		return err
	}

	emb := result.Embedding
	if emb.Rows != points.Rows || emb.Cols <= 0 || len(emb.Data) != emb.Rows*emb.Cols ||
		(len(emb.IDs) != 0 && len(emb.IDs) != emb.Rows) {
		return connect.NewError(connect.CodeInternal, errUmapResultMismatch)
	}

	if err := stream.Send(&vectoropspb.ProjectUmapResponse{
		Response: &vectoropspb.ProjectUmapResponse_Metadata_{
			Metadata: &vectoropspb.ProjectUmapResponse_Metadata{Rows: int32(emb.Rows), Cols: int32(emb.Cols)}, //nolint:gosec // test-fixture dimensions fit int32
		},
	}); err != nil {
		return err
	}

	for offset := 0; offset < emb.Rows; offset += vectorOpsRowsPerChunk {
		end := min(offset+vectorOpsRowsPerChunk, emb.Rows)

		chunk := &vectoropspb.MatrixChunk{Data: emb.Data[offset*emb.Cols : end*emb.Cols]}
		if len(emb.IDs) != 0 {
			chunk.Ids = emb.IDs[offset:end]
		}

		if err := stream.Send(&vectoropspb.ProjectUmapResponse{
			Response: &vectoropspb.ProjectUmapResponse_Embedding{Embedding: chunk},
		}); err != nil {
			return err
		}
	}

	return nil
}

// buildFacadeMatrix reshapes the accumulated flat data and ids into the
// facade Matrix, enforcing the proto's INVALID_ARGUMENT validation rules
// the way the real server does.
func buildFacadeMatrix(rows, cols int32, data []float32, ids []string) (vectorops.Matrix, error) {
	if rows <= 0 || cols <= 0 {
		return vectorops.Matrix{}, invalidArgument(errMatrixDims)
	}

	if int64(len(data)) != int64(rows)*int64(cols) {
		return vectorops.Matrix{}, invalidArgument(errMatrixDataLength)
	}

	if len(ids) != 0 && int64(len(ids)) != int64(rows) {
		return vectorops.Matrix{}, invalidArgument(errMatrixIDsLength)
	}

	return vectorops.Matrix{Data: data, Rows: int(rows), Cols: int(cols), IDs: ids}, nil
}

func hdbscanOptionsFromProto(h *vectoropspb.ClusterHdbscanRequest_Header) *vectorops.HdbscanOptions {
	return &vectorops.HdbscanOptions{
		MinClusterSize:          int(h.GetMinClusterSize()),
		MinSamples:              int(h.GetMinSamples()),
		ClusterSelectionEpsilon: h.ClusterSelectionEpsilon,
		Metric:                  distanceMetricFromProto(h.GetMetric()),
		ClusterSelectionMethod:  clusterSelectionMethodFromProto(h.GetClusterSelectionMethod()),
	}
}

func distanceMetricFromProto(m vectoropspb.DistanceMetric) vectorops.DistanceMetric {
	switch m {
	case vectoropspb.DistanceMetric_DISTANCE_METRIC_EUCLIDEAN:
		return vectorops.DistanceMetricEuclidean
	case vectoropspb.DistanceMetric_DISTANCE_METRIC_COSINE:
		return vectorops.DistanceMetricCosine
	case vectoropspb.DistanceMetric_DISTANCE_METRIC_MANHATTAN:
		return vectorops.DistanceMetricManhattan
	case vectoropspb.DistanceMetric_DISTANCE_METRIC_UNSPECIFIED:
		return vectorops.DistanceMetricDefault
	}

	return vectorops.DistanceMetricDefault
}

func clusterSelectionMethodFromProto(m vectoropspb.ClusterSelectionMethod) vectorops.ClusterSelectionMethod {
	switch m {
	case vectoropspb.ClusterSelectionMethod_CLUSTER_SELECTION_METHOD_EOM:
		return vectorops.ClusterSelectionMethodEOM
	case vectoropspb.ClusterSelectionMethod_CLUSTER_SELECTION_METHOD_LEAF:
		return vectorops.ClusterSelectionMethodLeaf
	case vectoropspb.ClusterSelectionMethod_CLUSTER_SELECTION_METHOD_UNSPECIFIED:
		return vectorops.ClusterSelectionMethodDefault
	}

	return vectorops.ClusterSelectionMethodDefault
}
