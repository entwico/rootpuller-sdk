// Package fleet wraps com.entwico.rootpuller.fleet.FleetService: the state of
// the worker fleet behind rootpuller-api, for dashboards. Executors (a GPU and
// its memory budget), units started and parked on them, the job queue with
// each job's wait reason, running jobs with their progress, recent jobs,
// events and percentiles. PinUnit keeps a unit running for a while, for using
// a worker directly.
//
// Every call needs a token with the fleet read scope (rootpuller:fleet:read by
// default); without it they fail with PermissionDenied.
package fleet

import (
	"context"
	"iter"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	fleetpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/fleet"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/fleet/fleetconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the FleetService.
type Service struct {
	rpc fleetconnect.FleetServiceClient
}

// NewService builds a FleetService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client) *Service {
	core := sdk.TransportCore()

	return &Service{rpc: fleetconnect.NewFleetServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// GetState returns the fleet as it is now.
func (s *Service) GetState(ctx context.Context) (*State, error) {
	resp, err := s.rpc.GetState(ctx, connect.NewRequest(&fleetpb.GetStateRequest{}))
	if err != nil {
		return nil, transport.WrapError(err, fleetconnect.FleetServiceGetStateProcedure)
	}

	st := stateFromProto(resp.Msg.GetState())

	return &st, nil
}

// PinUnit wakes the unit if it is cold and keeps it running for d (at most
// 12h), whatever its idle time; it is not parked to make room for other work
// meanwhile. Pinning a pinned unit replaces when its pin ends. The pin ends
// early when the unit fails to start or is scaled down by hand, and an api
// restart drops it. An unknown unit is NotFound, an always-on one
// FailedPrecondition, a duration out of range InvalidArgument.
func (s *Service) PinUnit(ctx context.Context, unit string, d time.Duration) (*Unit, error) {
	resp, err := s.rpc.PinUnit(ctx, connect.NewRequest(&fleetpb.PinUnitRequest{Unit: unit, Duration: durationpb.New(d)}))
	if err != nil {
		return nil, transport.WrapError(err, fleetconnect.FleetServicePinUnitProcedure)
	}

	u := unitFromProto(resp.Msg.GetUnit())

	return &u, nil
}

// UnpinUnit ends the unit's pin; it then parks after its idle time, or at
// once when other work needs its memory. A unit that is not pinned is left as
// it is.
func (s *Service) UnpinUnit(ctx context.Context, unit string) (*Unit, error) {
	resp, err := s.rpc.UnpinUnit(ctx, connect.NewRequest(&fleetpb.UnpinUnitRequest{Unit: unit}))
	if err != nil {
		return nil, transport.WrapError(err, fleetconnect.FleetServiceUnpinUnitProcedure)
	}

	u := unitFromProto(resp.Msg.GetUnit())

	return &u, nil
}

// Watch yields the state at once, then again after every change (at most a
// few times a second) and at least every ten seconds. It runs until ctx is
// cancelled, the loop breaks or the connection fails; a broken connection is
// the last value's error, so the caller decides whether to watch again.
func (s *Service) Watch(ctx context.Context) iter.Seq2[State, error] {
	stream, err := s.rpc.WatchState(ctx, connect.NewRequest(&fleetpb.WatchStateRequest{}))
	if err != nil {
		wrapped := transport.WrapError(err, fleetconnect.FleetServiceWatchStateProcedure)

		return func(yield func(State, error) bool) {
			yield(State{}, wrapped)
		}
	}

	return streamio.EventSeq(stream, fleetconnect.FleetServiceWatchStateProcedure, nil,
		func(pb *fleetpb.WatchStateResponse) (State, bool, error) {
			return stateFromProto(pb.GetState()), false, nil
		})
}
