package rootpullersdk

import (
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Backpressure is a shared client-side admission gate for the five
// rootpuller-backed services (chunker, embedding, rerank, vectorops,
// chef). Those services advertise their per-deployment capacity in-band
// (rate-limit trailers, shed statuses with retry hints); a Backpressure
// gate follows those signals with AIMD concurrency control, a shared shed
// pause, and a deep-outage circuit breaker — so batch workloads pace
// themselves to the server instead of hammering it.
//
// The server pools capacity per deployment across those five services, so
// create ONE Backpressure per deployment and pass it to every routable
// service client targeting it:
//
//	bp := rootpullersdk.NewBackpressure(rootpullersdk.BackpressureOptions{SeedConcurrency: 6})
//	chk := chunker.NewService(sdk, chunker.WithDeployment("local"), chunker.WithBackpressure(bp))
//	emb := embedding.NewService(sdk, embedding.WithDeployment("local"), embedding.WithBackpressure(bp))
//
// Per-call deployment overrides (ContextWithDeployment) bypass the gate's
// accuracy — calls still pass through it, but its learned capacity tracks
// the construction-time deployment.
//
// Composition with WithRetry: the gate admits each retry attempt
// individually and its shed pause delays them, so enabling both gives
// paced retries for free. Streams hold one slot for their lifetime.
type Backpressure struct {
	gate *transport.BackpressureGate
}

// BackpressureOptions configures NewBackpressure. Zero values take the
// defaults noted per field.
type BackpressureOptions struct {
	// SeedConcurrency is the in-flight cap used until the server first
	// advertises its real limit (default 4).
	SeedConcurrency int
}

// NewBackpressure builds a shareable admission gate. See Backpressure for
// how to scope it.
func NewBackpressure(opts BackpressureOptions) *Backpressure {
	return &Backpressure{gate: transport.NewBackpressureGate(opts.SeedConcurrency)}
}

// Gate exposes the internal gate to the SDK's own service packages. The
// returned type lives in an internal package — treat this method as
// SDK-internal.
func (b *Backpressure) Gate() *transport.BackpressureGate { return b.gate }
