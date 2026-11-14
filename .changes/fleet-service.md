---
bump: minor
---

New `fleet` package for rootpuller-api's FleetService: `GetState` and `Watch` report the worker fleet — executors and their GPU memory, units started and parked on demand, the job queue with each job's wait reason, running jobs with progress and caller, recent jobs, events and percentiles. Needs the `rootpuller:fleet:read` scope. `rootpullertest.Fleet` serves scripted states.
