package rootpullertest

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/entwico/rootpuller-sdk/fleet"
	fleetpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/fleet"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/fleet/fleetconnect"
)

var errFleetDenied = errors.New("the fleet state needs the rootpuller:fleet:read scope")

// Fleet is a facade-typed fake FleetService. GetState answers the last of
// States (an empty state without any); WatchState streams States in order and
// then stays open until the client leaves, as the real server does. Denied
// fails both with PermissionDenied, like a token without the read scope.
type Fleet struct {
	States []fleet.State
	Denied bool
}

func (f *Fleet) register(mux *http.ServeMux) {
	mux.Handle(fleetconnect.NewFleetServiceHandler(&fleetHandler{fake: f}))
}

type fleetHandler struct {
	fake *Fleet
}

func (h *fleetHandler) GetState(context.Context, *connect.Request[fleetpb.GetStateRequest]) (*connect.Response[fleetpb.GetStateResponse], error) {
	if h.fake.Denied {
		return nil, connect.NewError(connect.CodePermissionDenied, errFleetDenied)
	}

	var st fleet.State
	if n := len(h.fake.States); n > 0 {
		st = h.fake.States[n-1]
	}

	return connect.NewResponse(&fleetpb.GetStateResponse{State: fleetStateToProto(st)}), nil
}

func (h *fleetHandler) WatchState(
	ctx context.Context,
	_ *connect.Request[fleetpb.WatchStateRequest],
	stream *connect.ServerStream[fleetpb.WatchStateResponse],
) error {
	if h.fake.Denied {
		return connect.NewError(connect.CodePermissionDenied, errFleetDenied)
	}

	states := h.fake.States
	if len(states) == 0 {
		states = []fleet.State{{}}
	}

	for _, st := range states {
		if err := stream.Send(&fleetpb.WatchStateResponse{State: fleetStateToProto(st)}); err != nil {
			return err
		}
	}

	<-ctx.Done()

	return nil
}

func fleetTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}

	return timestamppb.New(t)
}

func fleetDuration(d time.Duration) *durationpb.Duration {
	if d == 0 {
		return nil
	}

	return durationpb.New(d)
}

var (
	fleetUnitStates = map[fleet.UnitState]fleetpb.UnitState{
		fleet.UnitCold:     fleetpb.UnitState_UNIT_STATE_COLD,
		fleet.UnitStarting: fleetpb.UnitState_UNIT_STATE_STARTING,
		fleet.UnitWarm:     fleetpb.UnitState_UNIT_STATE_WARM,
		fleet.UnitStopping: fleetpb.UnitState_UNIT_STATE_STOPPING,
	}
	fleetClasses = map[fleet.JobClass]fleetpb.JobClass{
		fleet.ClassBatch:    fleetpb.JobClass_JOB_CLASS_BATCH,
		fleet.ClassRealtime: fleetpb.JobClass_JOB_CLASS_REALTIME,
	}
	fleetOutcomes = map[fleet.Outcome]fleetpb.JobOutcome{
		fleet.OutcomeSucceeded: fleetpb.JobOutcome_JOB_OUTCOME_SUCCEEDED,
		fleet.OutcomeFailed:    fleetpb.JobOutcome_JOB_OUTCOME_FAILED,
		fleet.OutcomeCancelled: fleetpb.JobOutcome_JOB_OUTCOME_CANCELLED,
		fleet.OutcomeAbandoned: fleetpb.JobOutcome_JOB_OUTCOME_ABANDONED,
		fleet.OutcomeRejected:  fleetpb.JobOutcome_JOB_OUTCOME_REJECTED,
	}
)

//nolint:gosec // counts in a fake state are small
func fleetStateToProto(s fleet.State) *fleetpb.FleetState {
	out := &fleetpb.FleetState{At: fleetTime(s.At)}

	for _, e := range s.Executors {
		out.Executors = append(out.Executors, &fleetpb.Executor{
			Name: e.Name, Kind: e.Kind, GpuMemoryBytes: e.GPUMemoryBytes, GpuClaimedBytes: e.GPUClaimedBytes,
			GuardEnabled: e.GuardEnabled, MeasuredFreeBytes: e.MeasuredFreeBytes,
			MeasuredAt: fleetTime(e.MeasuredAt), GuardError: e.GuardError,
		})
	}

	for _, u := range s.Units {
		out.Units = append(out.Units, &fleetpb.Unit{
			Name: u.Name, Executor: u.Executor, Managed: u.Managed, State: fleetUnitStates[u.State],
			Since: fleetTime(u.Since), GpuMemoryBytes: u.GPUMemoryBytes, Jobs: int32(u.Jobs), Probes: int32(u.Probes),
			LastUsedAt: fleetTime(u.LastUsedAt), ParksAt: fleetTime(u.ParksAt), DrainedForJob: u.DrainedForJob,
			HeldReason: u.HeldReason, Starts: int32(u.Starts), Failures: int32(u.Failures), LastError: u.LastError,
			ColdStartP50: fleetDuration(u.ColdStartP50), ColdStartP95: fleetDuration(u.ColdStartP95),
		})
	}

	for _, w := range s.Workers {
		pw := &fleetpb.Worker{Name: w.Name, Spillover: w.Spillover}
		for _, d := range w.Deployments {
			pw.Deployments = append(pw.Deployments, &fleetpb.Deployment{
				Name: d.Name, Unit: d.Unit, MaxConcurrent: int32(d.MaxConcurrent), Active: int32(d.Active),
			})
		}

		out.Workers = append(out.Workers, pw)
	}

	for _, j := range s.Queue {
		out.Queue = append(out.Queue, fleetJobToProto(j))
	}

	for _, j := range s.Running {
		out.Running = append(out.Running, fleetJobToProto(j))
	}

	for _, r := range s.RecentJobs {
		out.RecentJobs = append(out.RecentJobs, &fleetpb.JobRecord{
			Id: r.ID, Worker: r.Worker, Deployment: r.Deployment, Unit: r.Unit, Kind: r.Kind, Caller: r.Caller,
			Class: fleetClasses[r.Class], Priority: int32(r.Priority), EnqueuedAt: fleetTime(r.EnqueuedAt),
			StartedAt: fleetTime(r.StartedAt), EndedAt: fleetTime(r.EndedAt), Outcome: fleetOutcomes[r.Outcome],
			ErrorCode: r.ErrorCode,
		})
	}

	for _, e := range s.Events {
		out.Events = append(out.Events, &fleetpb.Event{
			At: fleetTime(e.At), Kind: e.Kind, Executor: e.Executor, Unit: e.Unit, Message: e.Message,
		})
	}

	for _, st := range s.Stats {
		ps := &fleetpb.WorkerStats{
			Worker: st.Worker, WaitP50: fleetDuration(st.WaitP50), WaitP95: fleetDuration(st.WaitP95),
			RunP50: fleetDuration(st.RunP50), RunP95: fleetDuration(st.RunP95),
		}

		for o, n := range st.Outcomes {
			ps.Outcomes = append(ps.Outcomes, &fleetpb.OutcomeCount{Outcome: fleetOutcomes[o], Count: int32(n)})
		}

		out.Stats = append(out.Stats, ps)
	}

	return out
}

//nolint:gosec // counts in a fake state are small
func fleetJobToProto(j fleet.Job) *fleetpb.Job {
	return &fleetpb.Job{
		Id: j.ID, Worker: j.Worker, Deployment: j.Deployment, Unit: j.Unit, Kind: j.Kind, Caller: j.Caller,
		Class: fleetClasses[j.Class], Priority: int32(j.Priority), EnqueuedAt: fleetTime(j.EnqueuedAt),
		StartedAt: fleetTime(j.StartedAt), Position: int32(j.Position), WaitReason: j.WaitReason,
		Stage: j.Stage, Completed: j.Completed, Total: j.Total,
	}
}
