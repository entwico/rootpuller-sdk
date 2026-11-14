# Fleet: what the workers are doing

rootpuller-api queues every job of every worker (composer, painter, video, …)
and places it on a **unit** — a worker instance on an **executor**, such as the
composer Deployment on the shared GPU `gpu-01`. Units start on demand and are
parked when idle; on a shared GPU a unit starts only when its memory fits, so
others may be parked or drained first. `fleet` reports all of that, for
dashboards.

```go
svc := fleet.NewService(sdk)

st, err := svc.GetState(ctx)            // one snapshot

for st, err := range svc.Watch(ctx) {   // a snapshot after every change
	if err != nil {
		return err                      // the connection broke; watch again if you like
	}
	render(st)
}
```

`Watch` sends the state at once, then after every change (at most a few times a
second) and at least every ten seconds, so silence for longer than that means
the connection is gone. Every value is the full state: replace what you show.

## What the state holds

| field | what it is |
|---|---|
| `Executors` | capacity: a GPU's memory budget (`GPUMemoryBytes`), what the units that are not cold claim (`GPUClaimedBytes`), and — with a guard — what the card measurably has free |
| `Units` | each instance: `State` (cold, starting, warm, stopping), since when, its memory, jobs on it, `ParksAt` (when an idle unit will be parked), `DrainedForJob` (it takes no new work: a waiting job needs its memory), `HeldReason`, start count, failures and the last error with its cause, cold-start p50/p95 |
| `Workers` | each worker's deployments in preference order, the unit each runs on, concurrency cap and active jobs |
| `Queue` | waiting jobs in the order they will be placed, with `WaitReason` ("starting comfyui", "waiting for GPU memory held by composer", …) |
| `Running` | jobs holding a deployment: where, who (`Caller`), since when, and progress (`Stage`, `Completed`/`Total`) |
| `RecentJobs` | finished jobs, newest first: outcome (succeeded, failed, cancelled, abandoned while waiting, rejected), wait and run times, error code |
| `Events` | what happened to units, newest first: waking, ready, start_failed, held, parking, parked, stop_failed, adopted, vanished, unreachable, guard_unavailable, drained |
| `Stats` | per worker over the history: outcomes, wait and run p50/p95 |

History covers the last few hundred jobs and events since the api started.

## Access

Both calls need a token with the fleet read scope (`rootpuller:fleet:read` by
default); without it they fail with `PermissionDenied`. The state shows every
caller's jobs.

## Testing

`rootpullertest.Fleet` serves scripted states: `GetState` answers the last of
`States`, `Watch` yields them in order and then stays open until the client
leaves. `Denied: true` makes both fail with `PermissionDenied`.
