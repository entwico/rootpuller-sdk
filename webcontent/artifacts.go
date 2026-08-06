package webcontent

import (
	"fmt"

	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// ExtractionJob requests one output artifact kind (markdown, text, JSON,
// ...) from the fetched or uploaded HTML.
type ExtractionJob struct {
	Kind    ArtifactKind
	Options *ExtractionOptions
}

// ExtractionOptions tunes the trafilatura-based content extraction of one
// job. Nil fields keep the server defaults noted per field.
type ExtractionOptions struct {
	TargetLanguage    *string
	IncludeComments   *bool // default false
	IncludeTables     *bool // default true
	IncludeImages     *bool // default false
	IncludeLinks      *bool // default false
	IncludeFormatting *bool // default false
	Deduplicate       *bool // default true
	FavorPrecision    *bool
	FavorRecall       *bool
	Fast              *bool // skip fallback extractors
	PruneXPaths       []string
	OnlyWithMetadata  *bool
}

func (j ExtractionJob) toProto() (*webcontentpb.ExtractionJob, error) {
	kind, err := j.Kind.toProto()
	if err != nil {
		return nil, err
	}
	msg := &webcontentpb.ExtractionJob{Kind: kind}
	if o := j.Options; o != nil {
		msg.Options = &webcontentpb.ExtractionJob_Options{
			TargetLanguage:    o.TargetLanguage,
			IncludeComments:   o.IncludeComments,
			IncludeTables:     o.IncludeTables,
			IncludeImages:     o.IncludeImages,
			IncludeLinks:      o.IncludeLinks,
			IncludeFormatting: o.IncludeFormatting,
			Deduplicate:       o.Deduplicate,
			FavorPrecision:    o.FavorPrecision,
			FavorRecall:       o.FavorRecall,
			Fast:              o.Fast,
			PruneXpaths:       o.PruneXPaths,
			OnlyWithMetadata:  o.OnlyWithMetadata,
		}
	}
	return msg, nil
}

func jobsToProto(jobs []ExtractionJob) ([]*webcontentpb.ExtractionJob, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	out := make([]*webcontentpb.ExtractionJob, len(jobs))
	for i, j := range jobs {
		msg, err := j.toProto()
		if err != nil {
			return nil, err
		}
		out[i] = msg
	}
	return out, nil
}

// ScreenshotOptions requests a page screenshot alongside the fetch.
type ScreenshotOptions struct {
	Format       ScreenshotFormat
	FullPage     *bool
	Quality      *int // for lossy formats
	ClipSelector *string
}

func (s *ScreenshotOptions) toProto() (*webcontentpb.ScreenshotOptions, error) {
	if s == nil {
		return nil, nil
	}
	format, ok := screenshotFormatToProto[s.Format]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown screenshot format %q", s.Format))
	}
	return &webcontentpb.ScreenshotOptions{
		Format:       format,
		FullPage:     s.FullPage,
		Quality:      protoconv.Int32Ptr(s.Quality),
		ClipSelector: s.ClipSelector,
	}, nil
}
