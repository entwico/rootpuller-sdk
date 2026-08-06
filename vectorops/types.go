package vectorops

import (
	"fmt"
	"math"

	"connectrpc.com/connect"

	"github.com/entwico/rootpuller-sdk/internal/apierr"
	vectoropspb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/vectorops"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// Matrix is a dense row-major float32 matrix: Data holds Rows*Cols values,
// row after row. IDs optionally names each row (e.g. Qdrant point IDs) so
// results can be re-joined to their source points; when set, its length
// must equal Rows.
type Matrix struct {
	// Data is the row-major matrix content; len(Data) must equal Rows*Cols.
	Data []float32
	// Rows and Cols are the matrix dimensions.
	Rows, Cols int
	// IDs optionally labels each row; empty or exactly Rows entries.
	IDs []string
}

func (m *Matrix) validate() error {
	if m.Rows < 0 || m.Cols < 0 || m.Rows > math.MaxInt32 || m.Cols > math.MaxInt32 {
		return invalidArgument(fmt.Sprintf("matrix dimensions %dx%d out of range; rows and cols must be non-negative and fit int32", m.Rows, m.Cols))
	}

	if len(m.Data) != m.Rows*m.Cols {
		return invalidArgument(fmt.Sprintf("matrix data length %d does not equal rows*cols = %d*%d", len(m.Data), m.Rows, m.Cols))
	}

	if len(m.IDs) != 0 && len(m.IDs) != m.Rows {
		return invalidArgument(fmt.Sprintf("matrix has %d ids for %d rows; ids must be empty or one per row", len(m.IDs), m.Rows))
	}

	return nil
}

// DistanceMetric selects the input-space distance. The zero value keeps
// the server default (Euclidean). Note the server rejects Cosine for
// HDBSCAN: L2-normalise upstream and use Euclidean instead, which is
// order-equivalent to cosine on the unit sphere. UMAP supports all three.
type DistanceMetric string

const (
	DistanceMetricDefault   DistanceMetric = ""
	DistanceMetricEuclidean DistanceMetric = "euclidean"
	DistanceMetricCosine    DistanceMetric = "cosine"
	DistanceMetricManhattan DistanceMetric = "manhattan"
)

var distanceMetricToProto = map[DistanceMetric]vectoropspb.DistanceMetric{
	DistanceMetricDefault:   vectoropspb.DistanceMetric_DISTANCE_METRIC_UNSPECIFIED,
	DistanceMetricEuclidean: vectoropspb.DistanceMetric_DISTANCE_METRIC_EUCLIDEAN,
	DistanceMetricCosine:    vectoropspb.DistanceMetric_DISTANCE_METRIC_COSINE,
	DistanceMetricManhattan: vectoropspb.DistanceMetric_DISTANCE_METRIC_MANHATTAN,
}

func (d DistanceMetric) toProto() (vectoropspb.DistanceMetric, error) {
	v, ok := distanceMetricToProto[d]
	if !ok {
		return 0, invalidArgument(fmt.Sprintf("unknown distance metric %q", d))
	}

	return v, nil
}

// ClusterSelectionMethod picks flat clusters from HDBSCAN's condensed
// tree. The zero value keeps the server default (EOM, excess of mass).
type ClusterSelectionMethod string

const (
	ClusterSelectionMethodDefault ClusterSelectionMethod = ""
	ClusterSelectionMethodEOM     ClusterSelectionMethod = "eom"
	ClusterSelectionMethodLeaf    ClusterSelectionMethod = "leaf"
)

var clusterSelectionMethodToProto = map[ClusterSelectionMethod]vectoropspb.ClusterSelectionMethod{
	ClusterSelectionMethodDefault: vectoropspb.ClusterSelectionMethod_CLUSTER_SELECTION_METHOD_UNSPECIFIED,
	ClusterSelectionMethodEOM:     vectoropspb.ClusterSelectionMethod_CLUSTER_SELECTION_METHOD_EOM,
	ClusterSelectionMethodLeaf:    vectoropspb.ClusterSelectionMethod_CLUSTER_SELECTION_METHOD_LEAF,
}

func (m ClusterSelectionMethod) toProto() (vectoropspb.ClusterSelectionMethod, error) {
	v, ok := clusterSelectionMethodToProto[m]
	if !ok {
		return 0, invalidArgument(fmt.Sprintf("unknown cluster selection method %q", m))
	}

	return v, nil
}

func invalidArgument(msg string) error {
	return apierr.New(connect.CodeInvalidArgument, msg, "", 0, nil)
}

// HdbscanOptions tunes ClusterHdbscan. Nil and zero-value fields keep
// the server defaults noted per field.
type HdbscanOptions struct {
	// MinClusterSize is the smallest grouping that counts as a cluster
	// (0 keeps the server default, 5). For topic clustering over article
	// embeddings 10-25 tends to suppress micro-topics.
	MinClusterSize int
	// MinSamples sets how conservative the clustering is: larger values
	// push more points into noise. 0 keeps the server default
	// (MinClusterSize).
	MinSamples int
	// ClusterSelectionEpsilon merges clusters closer than this threshold;
	// nil keeps the server default (0, no merge).
	ClusterSelectionEpsilon *float32
	// Metric is the distance used to build the tree; Cosine is rejected
	// for HDBSCAN (see DistanceMetric).
	Metric DistanceMetric
	// ClusterSelectionMethod picks flat clusters from the condensed tree.
	ClusterSelectionMethod ClusterSelectionMethod
}

func (p *HdbscanOptions) toHeader(points *Matrix) (*vectoropspb.ClusterHdbscanRequest_Header, error) {
	if p == nil {
		p = &HdbscanOptions{}
	}

	metric, err := p.Metric.toProto()
	if err != nil {
		return nil, err
	}

	method, err := p.ClusterSelectionMethod.toProto()
	if err != nil {
		return nil, err
	}

	header := &vectoropspb.ClusterHdbscanRequest_Header{
		// The dimensions were bounds-checked against int32 in validate().
		Rows:                    protoconv.ClampInt32(int64(points.Rows)),
		Cols:                    protoconv.ClampInt32(int64(points.Cols)),
		ClusterSelectionEpsilon: p.ClusterSelectionEpsilon,
		Metric:                  metric,
		ClusterSelectionMethod:  method,
	}
	if p.MinClusterSize > 0 {
		header.MinClusterSize = protoconv.Int32Ptr(&p.MinClusterSize)
	}

	if p.MinSamples > 0 {
		header.MinSamples = protoconv.Int32Ptr(&p.MinSamples)
	}

	return header, nil
}

// HdbscanResult is the per-point clustering output. All slices have one
// entry per input row, in input order.
type HdbscanResult struct {
	// NumClusters is the number of discovered clusters, excluding the
	// noise label. 0 is valid (every point labelled noise).
	NumClusters int
	// Labels[i] is the cluster id of input row i; -1 means noise. Cluster
	// ids are contiguous from 0.
	Labels []int32
	// Probabilities[i] in [0, 1] is the strength of row i's membership in
	// its assigned cluster (1 = core point, 0 for noise).
	Probabilities []float32
	// OutlierScores[i] in [0, 1] is the GLOSH outlier score, independent
	// of the hard label.
	OutlierScores []float32
	// IDs echoes the input row ids when they were supplied, else empty.
	IDs []string
}

// UmapOptions tunes ProjectUmap. Nil and zero-value fields keep the
// server defaults noted per field.
type UmapOptions struct {
	// NNeighbors balances local against global structure (0 keeps the
	// server default, 15).
	NNeighbors int
	// MinDist is the minimum spacing between output points; nil keeps the
	// server default (0.1).
	MinDist *float32
	// NComponents is the target dimensionality (0 keeps the server
	// default, 2).
	NComponents int
	// Metric is the input-space distance; Cosine is supported natively and
	// preferred for raw un-normalised embeddings.
	Metric DistanceMetric
	// RandomState seeds the layout for reproducible output (0 is a valid
	// seed); setting it disables UMAP's internal parallelism. Nil keeps
	// the unseeded server default.
	RandomState *int
	// SupervisedLabels optionally guides the projection (supervised UMAP);
	// when set, its length must equal the points row count.
	SupervisedLabels []int32
}

func (p *UmapOptions) toHeader(points *Matrix) (*vectoropspb.ProjectUmapRequest_Header, error) {
	if p == nil {
		p = &UmapOptions{}
	}

	metric, err := p.Metric.toProto()
	if err != nil {
		return nil, err
	}

	header := &vectoropspb.ProjectUmapRequest_Header{
		// The dimensions were bounds-checked against int32 in validate().
		Rows:        protoconv.ClampInt32(int64(points.Rows)),
		Cols:        protoconv.ClampInt32(int64(points.Cols)),
		MinDist:     p.MinDist,
		Metric:      metric,
		RandomState: protoconv.Int32Ptr(p.RandomState),
	}
	if p.NNeighbors > 0 {
		header.NNeighbors = protoconv.Int32Ptr(&p.NNeighbors)
	}

	if p.NComponents > 0 {
		header.NComponents = protoconv.Int32Ptr(&p.NComponents)
	}

	return header, nil
}

// UmapResult is the projection output.
type UmapResult struct {
	// Embedding has the input row count and the requested NComponents as
	// columns; IDs echoes the input row ids when they were supplied.
	Embedding Matrix
}
