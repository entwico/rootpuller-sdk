package fleet

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	fleetpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/fleet"
)

// UnitState is where a unit is in its life.
type UnitState string

const (
	UnitUnknown UnitState = ""
	// UnitCold is not running.
	UnitCold UnitState = "cold"
	// UnitStarting was asked for and waits for its health check.
	UnitStarting UnitState = "starting"
	// UnitWarm takes jobs.
	UnitWarm UnitState = "warm"
	// UnitStopping is being parked; it holds its memory until its pods are gone.
	UnitStopping UnitState = "stopping"
)

// JobClass separates jobs a person waits on from jobs that can wait.
type JobClass string

const (
	ClassUnknown  JobClass = ""
	ClassBatch    JobClass = "batch"
	ClassRealtime JobClass = "realtime"
)

// Outcome is how a job ended.
type Outcome string

const (
	OutcomeUnknown   Outcome = ""
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	// OutcomeCancelled: the caller cancelled while the job ran.
	OutcomeCancelled Outcome = "cancelled"
	// OutcomeAbandoned: the caller left while the job waited.
	OutcomeAbandoned Outcome = "abandoned"
	// OutcomeRejected: no deployment could take the job.
	OutcomeRejected Outcome = "rejected"
)

// State is the fleet at one moment.
type State struct {
	At        time.Time
	Executors []Executor
	Units     []Unit
	Workers   []Worker

	// Queue is in the order the jobs will be placed.
	Queue []Job
	// Running is oldest first.
	Running []Job
	// RecentJobs and Events are newest first.
	RecentJobs []JobRecord
	Events     []Event

	Stats []WorkerStats
}

// Executor is a place capacity lives, such as one GPU and its memory budget.
type Executor struct {
	Name string
	// Kind is kubernetes (units scaled in the cluster) or static (always on).
	Kind string

	// GPUMemoryBytes is the budget its units share; 0 means no accounting.
	GPUMemoryBytes  int64
	GPUClaimedBytes int64

	// The guard measures what is really free; a start is held while it
	// measures less than the unit claims.
	GuardEnabled      bool
	MeasuredFreeBytes int64
	MeasuredAt        time.Time
	GuardError        string
}

// Unit is one instance started and parked on an executor.
type Unit struct {
	Name     string
	Executor string
	// Managed is false for an always-on server the fleet never touches.
	Managed bool
	State   UnitState
	Since   time.Time

	GPUMemoryBytes int64
	Jobs           int
	Probes         int
	LastUsedAt     time.Time

	// ParksAt is when an idle warm unit will be parked; zero otherwise.
	ParksAt time.Time
	// DrainedForJob is the waiting job this unit makes room for.
	DrainedForJob string
	// HeldReason says why a start is held, while it is.
	HeldReason string

	Starts   int
	Failures int
	// LastError is the last start or stop failure, with its cause.
	LastError string

	ColdStartP50 time.Duration
	ColdStartP95 time.Duration

	// PinnedUntil is when the unit's pin ends; zero when it is not pinned.
	PinnedUntil time.Time
	// PinWaitReason says why a pinned unit has not been started yet.
	PinWaitReason string
}

// Worker is one kind of work and its deployments, default first.
type Worker struct {
	Name        string
	Spillover   string
	Deployments []Deployment
}

// Deployment is a worker's endpoint on a unit.
type Deployment struct {
	Name string
	Unit string
	// MaxConcurrent is 0 for no cap.
	MaxConcurrent int
	Active        int
}

// Job is a waiting or running job.
type Job struct {
	ID     string
	Worker string
	// Deployment and Unit are set once the job runs.
	Deployment string
	Unit       string
	// Kind names the work, e.g. "composer.ComposeSong".
	Kind string
	// Caller is the token subject that asked.
	Caller   string
	Class    JobClass
	Priority int

	EnqueuedAt time.Time
	StartedAt  time.Time

	// Waiting jobs: the place in the queue (0 is next) and what it waits for.
	Position   int
	WaitReason string

	// Running jobs: progress as the worker reports it.
	Stage     string
	Completed int64
	Total     int64
}

// JobRecord is a finished job.
type JobRecord struct {
	ID         string
	Worker     string
	Deployment string
	Unit       string
	Kind       string
	Caller     string
	Class      JobClass
	Priority   int
	EnqueuedAt time.Time
	// StartedAt is zero when the job never ran.
	StartedAt time.Time
	EndedAt   time.Time
	Outcome   Outcome
	// ErrorCode is the gRPC code name of a failure, e.g. "unavailable".
	ErrorCode string
}

// Event is one thing that happened to a unit or the fleet. Kind is one of
// waking, ready, start_failed, held, parking, parked, stop_failed, adopted,
// vanished, unreachable, guard_unavailable, drained.
type Event struct {
	At       time.Time
	Kind     string
	Executor string
	Unit     string
	Message  string
}

// WorkerStats summarizes a worker's jobs in the history.
type WorkerStats struct {
	Worker   string
	Outcomes map[Outcome]int
	WaitP50  time.Duration
	WaitP95  time.Duration
	RunP50   time.Duration
	RunP95   time.Duration
}

var unitStates = map[fleetpb.UnitState]UnitState{
	fleetpb.UnitState_UNIT_STATE_COLD:     UnitCold,
	fleetpb.UnitState_UNIT_STATE_STARTING: UnitStarting,
	fleetpb.UnitState_UNIT_STATE_WARM:     UnitWarm,
	fleetpb.UnitState_UNIT_STATE_STOPPING: UnitStopping,
}

var classes = map[fleetpb.JobClass]JobClass{
	fleetpb.JobClass_JOB_CLASS_BATCH:    ClassBatch,
	fleetpb.JobClass_JOB_CLASS_REALTIME: ClassRealtime,
}

var outcomes = map[fleetpb.JobOutcome]Outcome{
	fleetpb.JobOutcome_JOB_OUTCOME_SUCCEEDED: OutcomeSucceeded,
	fleetpb.JobOutcome_JOB_OUTCOME_FAILED:    OutcomeFailed,
	fleetpb.JobOutcome_JOB_OUTCOME_CANCELLED: OutcomeCancelled,
	fleetpb.JobOutcome_JOB_OUTCOME_ABANDONED: OutcomeAbandoned,
	fleetpb.JobOutcome_JOB_OUTCOME_REJECTED:  OutcomeRejected,
}

func timeOf(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}

	return ts.AsTime()
}

func durationOf(d *durationpb.Duration) time.Duration {
	if d == nil {
		return 0
	}

	return d.AsDuration()
}

func unitFromProto(u *fleetpb.Unit) Unit {
	return Unit{
		Name:           u.GetName(),
		Executor:       u.GetExecutor(),
		Managed:        u.GetManaged(),
		State:          unitStates[u.GetState()],
		Since:          timeOf(u.GetSince()),
		GPUMemoryBytes: u.GetGpuMemoryBytes(),
		Jobs:           int(u.GetJobs()),
		Probes:         int(u.GetProbes()),
		LastUsedAt:     timeOf(u.GetLastUsedAt()),
		ParksAt:        timeOf(u.GetParksAt()),
		DrainedForJob:  u.GetDrainedForJob(),
		HeldReason:     u.GetHeldReason(),
		Starts:         int(u.GetStarts()),
		Failures:       int(u.GetFailures()),
		LastError:      u.GetLastError(),
		ColdStartP50:   durationOf(u.GetColdStartP50()),
		ColdStartP95:   durationOf(u.GetColdStartP95()),
		PinnedUntil:    timeOf(u.GetPinnedUntil()),
		PinWaitReason:  u.GetPinWaitReason(),
	}
}

func stateFromProto(pb *fleetpb.FleetState) State {
	s := State{At: timeOf(pb.GetAt())}

	for _, e := range pb.GetExecutors() {
		s.Executors = append(s.Executors, Executor{
			Name:              e.GetName(),
			Kind:              e.GetKind(),
			GPUMemoryBytes:    e.GetGpuMemoryBytes(),
			GPUClaimedBytes:   e.GetGpuClaimedBytes(),
			GuardEnabled:      e.GetGuardEnabled(),
			MeasuredFreeBytes: e.GetMeasuredFreeBytes(),
			MeasuredAt:        timeOf(e.GetMeasuredAt()),
			GuardError:        e.GetGuardError(),
		})
	}

	for _, u := range pb.GetUnits() {
		s.Units = append(s.Units, unitFromProto(u))
	}

	for _, w := range pb.GetWorkers() {
		worker := Worker{Name: w.GetName(), Spillover: w.GetSpillover()}

		for _, d := range w.GetDeployments() {
			worker.Deployments = append(worker.Deployments, Deployment{
				Name: d.GetName(), Unit: d.GetUnit(), MaxConcurrent: int(d.GetMaxConcurrent()), Active: int(d.GetActive()),
			})
		}

		s.Workers = append(s.Workers, worker)
	}

	for _, j := range pb.GetQueue() {
		s.Queue = append(s.Queue, jobFromProto(j))
	}

	for _, j := range pb.GetRunning() {
		s.Running = append(s.Running, jobFromProto(j))
	}

	for _, r := range pb.GetRecentJobs() {
		s.RecentJobs = append(s.RecentJobs, JobRecord{
			ID:         r.GetId(),
			Worker:     r.GetWorker(),
			Deployment: r.GetDeployment(),
			Unit:       r.GetUnit(),
			Kind:       r.GetKind(),
			Caller:     r.GetCaller(),
			Class:      classes[r.GetClass()],
			Priority:   int(r.GetPriority()),
			EnqueuedAt: timeOf(r.GetEnqueuedAt()),
			StartedAt:  timeOf(r.GetStartedAt()),
			EndedAt:    timeOf(r.GetEndedAt()),
			Outcome:    outcomes[r.GetOutcome()],
			ErrorCode:  r.GetErrorCode(),
		})
	}

	for _, e := range pb.GetEvents() {
		s.Events = append(s.Events, Event{
			At: timeOf(e.GetAt()), Kind: e.GetKind(), Executor: e.GetExecutor(), Unit: e.GetUnit(), Message: e.GetMessage(),
		})
	}

	for _, st := range pb.GetStats() {
		ws := WorkerStats{
			Worker:   st.GetWorker(),
			Outcomes: map[Outcome]int{},
			WaitP50:  durationOf(st.GetWaitP50()),
			WaitP95:  durationOf(st.GetWaitP95()),
			RunP50:   durationOf(st.GetRunP50()),
			RunP95:   durationOf(st.GetRunP95()),
		}

		for _, o := range st.GetOutcomes() {
			ws.Outcomes[outcomes[o.GetOutcome()]] = int(o.GetCount())
		}

		s.Stats = append(s.Stats, ws)
	}

	return s
}

func jobFromProto(j *fleetpb.Job) Job {
	return Job{
		ID:         j.GetId(),
		Worker:     j.GetWorker(),
		Deployment: j.GetDeployment(),
		Unit:       j.GetUnit(),
		Kind:       j.GetKind(),
		Caller:     j.GetCaller(),
		Class:      classes[j.GetClass()],
		Priority:   int(j.GetPriority()),
		EnqueuedAt: timeOf(j.GetEnqueuedAt()),
		StartedAt:  timeOf(j.GetStartedAt()),
		Position:   int(j.GetPosition()),
		WaitReason: j.GetWaitReason(),
		Stage:      j.GetStage(),
		Completed:  j.GetCompleted(),
		Total:      j.GetTotal(),
	}
}
