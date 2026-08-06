package vectorops_test

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
	"github.com/entwico/rootpuller-sdk/vectorops"
)

var errGPUBusy = errors.New("gpu busy")

func newService(t *testing.T, baseURL string) *vectorops.Service {
	t.Helper()

	sdk, err := rootpullersdk.New(baseURL)
	if err != nil {
		t.Fatal(err)
	}

	return vectorops.NewService(sdk)
}

// testMatrix builds a rows x cols matrix with deterministic content and
// one id per row.
func testMatrix(rows, cols int) vectorops.Matrix {
	m := vectorops.Matrix{Rows: rows, Cols: cols}

	m.Data = make([]float32, rows*cols)
	for i := range m.Data {
		m.Data[i] = float32(i % 997)
	}

	m.IDs = make([]string, rows)
	for i := range m.IDs {
		m.IDs[i] = fmt.Sprintf("point-%d", i)
	}

	return m
}

func TestClusterHdbscanRoundTrip(t *testing.T) {
	t.Parallel()

	// 5000 rows x 128 cols = 640k floats: multiple request data frames, a
	// separate ids frame, and five row-aligned response chunks.
	const rows, cols = 5000, 128

	var (
		gotPoints vectorops.Matrix
		gotOpts   *vectorops.HdbscanOptions
	)

	srv := rootpullertest.NewServer(t, &rootpullertest.VectorOps{
		ClusterFunc: func(points vectorops.Matrix, opts *vectorops.HdbscanOptions) (*vectorops.HdbscanResult, error) {
			gotPoints = points
			gotOpts = opts

			result := &vectorops.HdbscanResult{
				NumClusters:   3,
				Labels:        make([]int32, points.Rows),
				Probabilities: make([]float32, points.Rows),
				OutlierScores: make([]float32, points.Rows),
				IDs:           points.IDs,
			}
			for i := range result.Labels {
				result.Labels[i] = int32(i%4) - 1 // -1 (noise), 0, 1, 2
				result.Probabilities[i] = float32(i%4) / 4
				result.OutlierScores[i] = 1 - float32(i%4)/4
			}

			return result, nil
		},
	})

	svc := newService(t, srv.URL)
	points := testMatrix(rows, cols)

	result, err := svc.ClusterHdbscan(t.Context(), points, &vectorops.HdbscanOptions{
		MinClusterSize:         15,
		Metric:                 vectorops.DistanceMetricEuclidean,
		ClusterSelectionMethod: vectorops.ClusterSelectionMethodLeaf,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The server must have reassembled the exact matrix.
	if gotPoints.Rows != rows || gotPoints.Cols != cols {
		t.Errorf("server saw %dx%d matrix, want %dx%d", gotPoints.Rows, gotPoints.Cols, rows, cols)
	}

	if len(gotPoints.Data) != rows*cols || gotPoints.Data[rows*cols-1] != points.Data[rows*cols-1] {
		t.Errorf("server saw %d data values (last %v), want %d (last %v)",
			len(gotPoints.Data), gotPoints.Data[len(gotPoints.Data)-1], rows*cols, points.Data[rows*cols-1])
	}

	if len(gotPoints.IDs) != rows || gotPoints.IDs[0] != "point-0" || gotPoints.IDs[rows-1] != "point-4999" {
		t.Errorf("server saw %d ids (first %q)", len(gotPoints.IDs), gotPoints.IDs[0])
	}

	if gotOpts == nil || gotOpts.MinClusterSize != 15 {
		t.Errorf("server saw options %+v, want MinClusterSize 15", gotOpts)
	}

	if gotOpts.Metric != vectorops.DistanceMetricEuclidean || gotOpts.ClusterSelectionMethod != vectorops.ClusterSelectionMethodLeaf {
		t.Errorf("server saw metric %q method %q", gotOpts.Metric, gotOpts.ClusterSelectionMethod)
	}

	// The client must have reassembled the full per-point arrays.
	if result.NumClusters != 3 {
		t.Errorf("NumClusters = %d, want 3", result.NumClusters)
	}

	if len(result.Labels) != rows || len(result.Probabilities) != rows || len(result.OutlierScores) != rows {
		t.Fatalf("got %d labels / %d probabilities / %d outlier scores, want %d each",
			len(result.Labels), len(result.Probabilities), len(result.OutlierScores), rows)
	}

	if len(result.IDs) != rows || result.IDs[rows-1] != "point-4999" {
		t.Errorf("got %d result ids", len(result.IDs))
	}

	if result.Labels[0] != -1 || result.Labels[4999] != 2 {
		t.Errorf("Labels[0] = %d, Labels[4999] = %d, want -1 and 2", result.Labels[0], result.Labels[4999])
	}
}

func TestProjectUmapRoundTrip(t *testing.T) {
	t.Parallel()

	const rows, cols = 2500, 64

	var gotLabels []int32

	srv := rootpullertest.NewServer(t, &rootpullertest.VectorOps{
		UmapFunc: func(points vectorops.Matrix, labels []int32) (*vectorops.UmapResult, error) {
			gotLabels = labels
			out := vectorops.Matrix{Rows: points.Rows, Cols: 3, IDs: points.IDs}

			out.Data = make([]float32, out.Rows*out.Cols)
			for i := range out.Data {
				out.Data[i] = float32(i)
			}

			return &vectorops.UmapResult{Embedding: out}, nil
		},
	})

	svc := newService(t, srv.URL)
	points := testMatrix(rows, cols)

	labels := make([]int32, rows)
	for i := range labels {
		labels[i] = int32(i % 7)
	}

	result, err := svc.ProjectUmap(t.Context(), points, &vectorops.UmapOptions{
		NComponents:      3,
		Metric:           vectorops.DistanceMetricCosine,
		SupervisedLabels: labels,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(gotLabels) != rows || gotLabels[rows-1] != int32((rows-1)%7) {
		t.Errorf("server saw %d supervised labels", len(gotLabels))
	}

	emb := result.Embedding
	if emb.Rows != rows || emb.Cols != 3 {
		t.Errorf("embedding is %dx%d, want %dx3", emb.Rows, emb.Cols, rows)
	}

	if len(emb.Data) != rows*3 || emb.Data[rows*3-1] != float32(rows*3-1) {
		t.Errorf("got %d embedding values", len(emb.Data))
	}

	if len(emb.IDs) != rows || emb.IDs[0] != "point-0" || emb.IDs[rows-1] != "point-2499" {
		t.Errorf("got %d embedding ids (first %q)", len(emb.IDs), emb.IDs[0])
	}
}

func TestMatrixValidationFailsLocally(t *testing.T) {
	t.Parallel()

	// No server: local validation must fail before any dial.
	svc := newService(t, "http://127.0.0.1:1")

	// Data length disagreeing with rows*cols.
	bad := vectorops.Matrix{Data: make([]float32, 10), Rows: 3, Cols: 4}
	if _, err := svc.ClusterHdbscan(t.Context(), bad, nil); !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Errorf("ClusterHdbscan(data mismatch) err = %v, want ErrInvalidArgument", err)
	}

	if _, err := svc.ProjectUmap(t.Context(), bad, nil); !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Errorf("ProjectUmap(data mismatch) err = %v, want ErrInvalidArgument", err)
	}

	// IDs length disagreeing with rows.
	badIDs := vectorops.Matrix{Data: make([]float32, 12), Rows: 3, Cols: 4, IDs: []string{"only-one"}}
	if _, err := svc.ClusterHdbscan(t.Context(), badIDs, nil); !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Errorf("ClusterHdbscan(ids mismatch) err = %v, want ErrInvalidArgument", err)
	}

	// Supervised labels length disagreeing with rows.
	good := vectorops.Matrix{Data: make([]float32, 12), Rows: 3, Cols: 4}
	if _, err := svc.ProjectUmap(t.Context(), good, &vectorops.UmapOptions{SupervisedLabels: []int32{1}}); !errors.Is(err, rootpullersdk.ErrInvalidArgument) {
		t.Errorf("ProjectUmap(labels mismatch) err = %v, want ErrInvalidArgument", err)
	}
}

func TestServerErrorSurfacesAsAPIError(t *testing.T) {
	t.Parallel()

	srv := rootpullertest.NewServer(t, &rootpullertest.VectorOps{
		ClusterFunc: func(vectorops.Matrix, *vectorops.HdbscanOptions) (*vectorops.HdbscanResult, error) {
			return nil, connect.NewError(connect.CodeResourceExhausted, errGPUBusy)
		},
	})

	svc := newService(t, srv.URL)
	_, err := svc.ClusterHdbscan(t.Context(), testMatrix(10, 4), nil)

	apiErr, ok := errors.AsType[*rootpullersdk.Error](err)
	if !ok {
		t.Fatalf("err = %T (%v), want *rootpullersdk.Error", err, err)
	}

	if apiErr.Code != connect.CodeResourceExhausted {
		t.Errorf("Code = %v, want ResourceExhausted", apiErr.Code)
	}

	if !errors.Is(err, rootpullersdk.ErrResourceExhausted) {
		t.Error("errors.Is(err, ErrResourceExhausted) = false")
	}
}
