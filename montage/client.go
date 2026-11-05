// Package montage wraps com.entwico.rootpuller.montage.MontageService: the
// final assembly of a production. Render takes a Timeline (shots joined by
// cuts or crossfades, voice-overs and music laid over them, captions and logos
// on top) plus every asset it names, and returns one MP4.
//
// The pieces come from elsewhere: clips from the video package, voice-overs
// from voice, music from composer, stills and caption images from painter or
// your own renderer. A File from any of them becomes an asset through
// File.AsUpload.
package montage

import (
	"context"
	"fmt"
	"iter"
	"slices"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	rootpullersdk "github.com/entwico/rootpuller-sdk"
	"github.com/entwico/rootpuller-sdk/internal/apierr"
	commonpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/common"
	montagepb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/montage"
	"github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/montage/montageconnect"
	"github.com/entwico/rootpuller-sdk/internal/streamio"
	"github.com/entwico/rootpuller-sdk/internal/transport"
)

// Service calls the MontageService.
type Service struct {
	rpc montageconnect.MontageServiceClient
}

// NewService builds a MontageService client on the sdk connection.
func NewService(sdk *rootpullersdk.Client) *Service {
	core := sdk.TransportCore()

	return &Service{rpc: montageconnect.NewMontageServiceClient(core.HTTPClient, core.BaseURL, core.ClientOpts...)}
}

// Render renders timeline into one MP4. assets must hold exactly the ids the
// timeline names, each once. The call returns when the video is done;
// cancelling ctx stops the render. A timeline the server cannot render (a
// trim past a clip's end, a crossfade longer than a segment, mismatched
// aspect ratios, a limit from GetCapabilities) fails with InvalidArgument and
// a message that says what to fix.
func (s *Service) Render(ctx context.Context, timeline Timeline, assets []Asset, opts *RenderOptions) (*Result, error) {
	if opts == nil {
		opts = &RenderOptions{}
	}

	params, err := timelineToProto(timeline)
	if err != nil {
		return nil, err
	}

	if err := checkAssets(timeline, assets); err != nil {
		return nil, err
	}

	tails := make([]iter.Seq2[*montagepb.RenderRequest, error], 0, len(assets))

	for _, a := range assets {
		id := a.ID
		tails = append(tails, streamio.FileChunkFrames(a.Upload, func(c *commonpb.FileChunk) *montagepb.RenderRequest {
			// the chunk's name is how the server matches it to the timeline.
			c.Name = id

			return &montagepb.RenderRequest{Request: &montagepb.RenderRequest_Asset{Asset: c}}
		}))
	}

	procedure := montageconnect.MontageServiceRenderProcedure
	frames := streamio.Frames(&montagepb.RenderRequest{Request: &montagepb.RenderRequest_Params_{Params: &montagepb.RenderRequest_Params{Timeline: params}}}, tails...)

	var (
		files   streamio.FileCollector
		meta    Metadata
		gotMeta bool
	)

	err = streamio.UploadCollect(ctx, s.rpc.Render, procedure, frames, func(resp *montagepb.RenderResponse) error {
		switch {
		case resp.GetProgress() != nil:
			if opts.OnProgress != nil {
				opts.OnProgress(progressFromProto(resp.GetProgress()))
			}
		case resp.GetMetadata() != nil:
			meta = metadataFromProto(resp.GetMetadata())
			gotMeta = true
		case resp.GetFile() != nil:
			files.Add(resp.GetFile())
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	videos, err := files.Files()
	if err != nil {
		return nil, err
	}

	if !gotMeta || len(videos) != 1 {
		return nil, apierr.New(connect.CodeInternal, "the server sent no complete video", procedure, 0, nil)
	}

	return &Result{Video: videos[0], Metadata: meta}, nil
}

// GetCapabilities reports the server's render limits, for checking a
// timeline before uploading anything.
func (s *Service) GetCapabilities(ctx context.Context) (*Capabilities, error) {
	resp, err := s.rpc.GetCapabilities(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		return nil, transport.WrapError(err, montageconnect.MontageServiceGetCapabilitiesProcedure)
	}

	return capabilitiesFromProto(resp.Msg), nil
}

// ReferencedAssets lists every asset id the timeline names, in first-use
// order: the ids Render needs assets for.
func ReferencedAssets(t Timeline) []string {
	var ids []string

	add := func(id string) {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	for _, s := range t.Segments {
		add(s.Asset)
	}

	for _, a := range t.AudioTracks {
		add(a.Asset)
	}

	for _, o := range t.Overlays {
		add(o.Asset)
	}

	return ids
}

// checkAssets catches mismatched ids before any upload starts.
func checkAssets(t Timeline, assets []Asset) error {
	if err := checkTimelineIDs(t); err != nil {
		return err
	}

	referenced := ReferencedAssets(t)
	seen := make([]string, 0, len(assets))

	for _, a := range assets {
		if a.ID == "" {
			return invalidArgument("montage: an asset has no id")
		}

		if slices.Contains(seen, a.ID) {
			return invalidArgument(fmt.Sprintf("montage: asset %q is passed twice", a.ID))
		}

		if !slices.Contains(referenced, a.ID) {
			return invalidArgument(fmt.Sprintf("montage: asset %q is not used by the timeline", a.ID))
		}

		seen = append(seen, a.ID)
	}

	for _, id := range referenced {
		if !slices.Contains(seen, id) {
			return invalidArgument(fmt.Sprintf("montage: the timeline uses asset %q, which is not passed", id))
		}
	}

	return nil
}

// checkTimelineIDs names a timeline entry without an asset, which no asset
// passed to Render could fix.
func checkTimelineIDs(t Timeline) error {
	for i, s := range t.Segments {
		if s.Asset == "" {
			return invalidArgument(fmt.Sprintf("montage: segment %d has no asset", i))
		}
	}

	for i, a := range t.AudioTracks {
		if a.Asset == "" {
			return invalidArgument(fmt.Sprintf("montage: audio track %d has no asset", i))
		}
	}

	for i, o := range t.Overlays {
		if o.Asset == "" {
			return invalidArgument(fmt.Sprintf("montage: overlay %d has no asset", i))
		}
	}

	return nil
}
