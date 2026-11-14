// Package fleet wraps com.entwico.rootpuller.fleet.FleetService: the state of
// the worker fleet behind rootpuller-api, for dashboards. Executors (a GPU and
// its memory budget), units started and parked on them, the job queue with
// each job's wait reason, running jobs with their progress, recent jobs,
// events and percentiles.
//
// Both calls need a token with the fleet read scope (rootpuller:fleet:read by
// default); without it they fail with PermissionDenied.
package fleet

import (
	"context"
	"iter"

	"connectrpc.com/connect"

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
