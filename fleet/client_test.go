package fleet_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/fleet"
	"github.com/entwico/rootpuller-sdk/rootpullertest"
)

const (
	comfyui = "comfyui"
	painter = "painter"
)

var errUnknownUnit = errors.New("unknown unit")

func newService(t *testing.T, fake *rootpullertest.Fleet) *fleet.Service {
	t.Helper()

	srv := rootpullertest.NewServer(t, fake)

	sdk, err := rootpullersdk.New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	return fleet.NewService(sdk)
}

func sampleState() fleet.State {
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	return fleet.State{
		At: at,
		Executors: []fleet.Executor{{
			Name: "gpu-01", Kind: "kubernetes", GPUMemoryBytes: 18 << 30, GPUClaimedBytes: 13 << 30,
			GuardEnabled: true, MeasuredFreeBytes: 5 << 30, MeasuredAt: at,
		}},
		Units: []fleet.Unit{{
			Name: comfyui, Executor: "gpu-01", Managed: true, State: fleet.UnitWarm, Since: at,
			GPUMemoryBytes: 13 << 30, Jobs: 1, LastUsedAt: at, DrainedForJob: "J7",
			Starts: 2, ColdStartP50: 40 * time.Second, ColdStartP95: 70 * time.Second,
			PinnedUntil: at.Add(time.Hour),
		}},
		Workers: []fleet.Worker{{Name: painter, Spillover: "waitForPreferred", Deployments: []fleet.Deployment{
			{Name: "cluster", Unit: comfyui, MaxConcurrent: 2, Active: 1},
		}}},
		Queue: []fleet.Job{{
			ID: "J7", Worker: "composer", Kind: "composer.ComposeSong", Caller: "corpusrank",
			Class: fleet.ClassBatch, Priority: 2, EnqueuedAt: at, WaitReason: "waiting for GPU memory held by comfyui",
		}},
		Running: []fleet.Job{{
			ID: "J6", Worker: painter, Deployment: "cluster", Unit: comfyui, Kind: "painter.qwen-image-2.1",
			Class: fleet.ClassRealtime, Priority: 1, EnqueuedAt: at, StartedAt: at, Stage: "generating", Completed: 3, Total: 8,
		}},
		RecentJobs: []fleet.JobRecord{{
			ID: "J5", Worker: painter, Class: fleet.ClassBatch, Priority: 2, EnqueuedAt: at, EndedAt: at,
			Outcome: fleet.OutcomeAbandoned,
		}},
		Events: []fleet.Event{{At: at, Kind: "ready", Unit: comfyui, Message: "ready after 41s"}},
		Stats: []fleet.WorkerStats{{
			Worker: painter, Outcomes: map[fleet.Outcome]int{fleet.OutcomeSucceeded: 4},
			WaitP50: time.Second, RunP95: time.Minute,
		}},
	}
}

func TestGetState_ReturnsEveryField(t *testing.T) {
	t.Parallel()

	want := sampleState()

	got, err := newService(t, &rootpullertest.Fleet{States: []fleet.State{want}}).GetState(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("state differs after the round trip:\n got %+v\nwant %+v", *got, want)
	}
}

func TestWatch_YieldsEveryStateUntilTheLoopBreaks(t *testing.T) {
	t.Parallel()

	first, second := fleet.State{}, sampleState()
	svc := newService(t, &rootpullertest.Fleet{States: []fleet.State{first, second}})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var seen int

	for st, err := range svc.Watch(ctx) {
		if err != nil {
			t.Fatal(err)
		}

		seen++

		if seen == 2 {
			if len(st.Units) != 1 || st.Units[0].State != fleet.UnitWarm {
				t.Fatalf("second state = %+v", st.Units)
			}

			break
		}
	}

	if seen != 2 {
		t.Fatalf("saw %d states, want 2", seen)
	}
}

func TestFleet_WithoutTheScopeIsPermissionDenied(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Fleet{Denied: true})

	_, err := svc.GetState(context.Background())
	requireCode(t, err, connect.CodePermissionDenied)

	for _, err := range svc.Watch(context.Background()) {
		requireCode(t, err, connect.CodePermissionDenied)

		break
	}
	_, err = svc.PinUnit(context.Background(), comfyui, time.Hour)
	requireCode(t, err, connect.CodePermissionDenied)

	_, err = svc.UnpinUnit(context.Background(), comfyui)
	requireCode(t, err, connect.CodePermissionDenied)
}

func TestPinUnit_SendsTheUnitAndDuration(t *testing.T) {
	t.Parallel()

	var got []string

	until := time.Date(2026, 11, 19, 14, 0, 0, 0, time.UTC)
	svc := newService(t, &rootpullertest.Fleet{
		PinFunc: func(unit string, d time.Duration) (fleet.Unit, error) {
			got = append(got, unit+" "+d.String())

			return fleet.Unit{Name: unit, Managed: true, State: fleet.UnitCold, PinnedUntil: until,
				PinWaitReason: "waiting for GPU memory held by composer"}, nil
		},
		UnpinFunc: func(unit string) (fleet.Unit, error) {
			got = append(got, "unpin "+unit)

			return fleet.Unit{Name: unit, Managed: true, State: fleet.UnitWarm}, nil
		},
	})

	u, err := svc.PinUnit(context.Background(), comfyui, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if !u.PinnedUntil.Equal(until) || u.PinWaitReason != "waiting for GPU memory held by composer" {
		t.Fatalf("pinned unit = %+v", u)
	}

	u, err = svc.UnpinUnit(context.Background(), comfyui)
	if err != nil {
		t.Fatal(err)
	}

	if !u.PinnedUntil.IsZero() {
		t.Fatalf("unpinned unit still pinned until %v", u.PinnedUntil)
	}

	if want := []string{"comfyui 2h0m0s", "unpin comfyui"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestPinUnit_AnUnknownUnitIsNotFound(t *testing.T) {
	t.Parallel()

	svc := newService(t, &rootpullertest.Fleet{
		PinFunc: func(unit string, _ time.Duration) (fleet.Unit, error) {
			return fleet.Unit{}, connect.NewError(connect.CodeNotFound, fmt.Errorf("%w: %s", errUnknownUnit, unit))
		},
	})

	_, err := svc.PinUnit(context.Background(), "nope", time.Hour)
	requireCode(t, err, connect.CodeNotFound)
}

func requireCode(t *testing.T, err error, code connect.Code) {
	t.Helper()

	ae, ok := errors.AsType[*rootpullersdk.Error](err)
	if !ok {
		t.Fatalf("want *rootpullersdk.Error with %v, got %v", code, err)
	}

	if ae.Code != code {
		t.Fatalf("code = %v, want %v (%v)", ae.Code, code, err)
	}
}
