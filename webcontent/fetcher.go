package webcontent

import (
	"fmt"
	"time"

	webcontentpb "github.com/entwico/rootpuller-sdk/internal/gen/proto/com/entwico/rootpuller/webcontent"
	"github.com/entwico/rootpuller-sdk/internal/protoconv"
)

// Header is one HTTP header name/value pair. Multi-value headers repeat
// the name.
type Header struct {
	Name  string
	Value string
}

func headersToProto(hs []Header) []*webcontentpb.Header {
	if len(hs) == 0 {
		return nil
	}
	out := make([]*webcontentpb.Header, len(hs))
	for i, h := range hs {
		out[i] = &webcontentpb.Header{Name: h.Name, Value: h.Value}
	}
	return out
}

func headersFromProto(hs []*webcontentpb.Header) []Header {
	if len(hs) == 0 {
		return nil
	}
	out := make([]Header, len(hs))
	for i, h := range hs {
		out[i] = Header{Name: h.GetName(), Value: h.GetValue()}
	}
	return out
}

// EngineConfig is the engine-specific fetch configuration.
// Implementations: BasicConfig, DynamicConfig, StealthyConfig.
type EngineConfig interface{ isEngineConfig() }

// BasicConfig configures the plain HTTP fetch engine.
type BasicConfig struct {
	Method              Method
	Headers             []Header
	Cookies             map[string]string
	Body                *string // request body for POST/PUT
	Proxy               *string
	Timeout             *time.Duration // default 30s
	FollowRedirects     *bool          // default true
	MaxRedirects        *int           // default 30
	VerifySSL           *bool          // default true
	StealthyHeaders     *bool          // synthesize UA + Accept-*; default true
	GoogleSearchReferer *bool          // default false
}

func (BasicConfig) isEngineConfig() {}

// DynamicConfig configures the headless-browser fetch engine.
type DynamicConfig struct {
	Headless        *bool // default true
	RealChrome      *bool // requires Chrome on the server; default false
	Stealth         *bool // default true
	GoogleSearch    *bool
	NetworkIdle     *bool
	WaitForSelector *string
	Timeout         *time.Duration // default 30s
	Wait            *time.Duration // fixed wait after load
	Humanize        *bool
	DisableWebGL    *bool // default true
	HideWebdriver   *bool // default true
	ViewportWidth   *int
	ViewportHeight  *int
	Locale          *string
	UserAgent       *string
	Proxy           *string
	Cookies         map[string]string
	Headers         []Header
	CDPURL          *string // attach to a remote CDP endpoint
}

func (DynamicConfig) isEngineConfig() {}

// StealthyConfig configures the anti-bot-evasion fetch engine.
type StealthyConfig struct {
	Headless          *bool // default true
	BlockImages       *bool
	DisableResources  *bool // block CSS/JS/fonts/img; only safe without JS
	GoogleSearch      *bool
	NetworkIdle       *bool // recommended for SPAs
	WaitForSelector   *string
	WaitSelectorState *string // visible|attached|hidden|detached; default attached
	Wait              *time.Duration
	Timeout           *time.Duration // default 30s
	Humanize          *bool
	DisableWebGL      *bool // default true
	ViewportWidth     *int  // unset → randomised
	ViewportHeight    *int
	Locale            *string // e.g. "de-DE"
	Timezone          *string // e.g. "Europe/Berlin"
	OSFingerprint     *string // linux|macos|windows; unset → randomised
	Proxy             *string
	Cookies           map[string]string
	Headers           []Header
	SolveCloudflare   *bool // default false (blocked pages report BLOCKED_CLOUDFLARE)
}

func (StealthyConfig) isEngineConfig() {}

// FetcherOptions configures how a page is fetched. A nil *FetcherOptions
// keeps all server defaults.
type FetcherOptions struct {
	// Engine picks the fetch engine; EngineAuto lets FetchURL escalate
	// automatically. When Config is set and Engine is EngineAuto, the
	// engine is inferred from the config type.
	Engine Engine
	// Config carries engine-specific options; its concrete type must
	// match Engine when both are set.
	Config           EngineConfig
	Retries          *int
	RetryDelay       *time.Duration // default 1s
	UserAgent        *string
	ObeyRobotsTxt    *bool
	CrawlDelay       *time.Duration
	DownloadDelay    *time.Duration
	ETagHint         *string
	LastModifiedHint *string
}

func (f *FetcherOptions) toProto() (*webcontentpb.FetcherOptions, error) {
	if f == nil {
		return nil, nil
	}
	engine, ok := engineToProto[f.Engine]
	if !ok {
		return nil, invalidArgument(fmt.Sprintf("unknown fetch engine %q", f.Engine))
	}
	msg := &webcontentpb.FetcherOptions{
		Engine:           engine,
		Retries:          protoconv.Int32Ptr(f.Retries),
		RetryDelayMs:     protoconv.DurationToMsPtr(f.RetryDelay),
		UserAgent:        f.UserAgent,
		ObeyRobotsTxt:    f.ObeyRobotsTxt,
		CrawlDelayMs:     protoconv.DurationToMsPtr(f.CrawlDelay),
		DownloadDelayMs:  protoconv.DurationToMsPtr(f.DownloadDelay),
		EtagHint:         f.ETagHint,
		LastModifiedHint: f.LastModifiedHint,
	}
	switch cfg := f.Config.(type) {
	case nil:
	case BasicConfig:
		if err := requireEngine(f.Engine, EngineBasic); err != nil {
			return nil, err
		}
		basic := &webcontentpb.FetcherOptions_Basic{
			Headers:             headersToProto(cfg.Headers),
			Cookies:             cfg.Cookies,
			Body:                cfg.Body,
			Proxy:               cfg.Proxy,
			TimeoutSeconds:      protoconv.DurationToSecondsPtr(cfg.Timeout),
			FollowRedirects:     cfg.FollowRedirects,
			MaxRedirects:        protoconv.Int32Ptr(cfg.MaxRedirects),
			VerifySsl:           cfg.VerifySSL,
			StealthyHeaders:     cfg.StealthyHeaders,
			GoogleSearchReferer: cfg.GoogleSearchReferer,
		}
		if cfg.Method != MethodDefault {
			m, ok := methodToProto[cfg.Method]
			if !ok {
				return nil, invalidArgument(fmt.Sprintf("unknown HTTP method %q", cfg.Method))
			}
			basic.Method = &m
		}
		msg.Config = &webcontentpb.FetcherOptions_Basic_{Basic: basic}
		if msg.Engine == webcontentpb.FetcherOptions_ENGINE_UNSPECIFIED {
			msg.Engine = webcontentpb.FetcherOptions_ENGINE_BASIC
		}
	case DynamicConfig:
		if err := requireEngine(f.Engine, EngineDynamic); err != nil {
			return nil, err
		}
		msg.Config = &webcontentpb.FetcherOptions_Dynamic_{Dynamic: &webcontentpb.FetcherOptions_Dynamic{
			Headless:            cfg.Headless,
			RealChrome:          cfg.RealChrome,
			Stealth:             cfg.Stealth,
			GoogleSearch:        cfg.GoogleSearch,
			NetworkIdle:         cfg.NetworkIdle,
			WaitForSelector:     cfg.WaitForSelector,
			TimeoutMilliseconds: protoconv.DurationToMsPtr(cfg.Timeout),
			WaitMilliseconds:    protoconv.DurationToMsPtr(cfg.Wait),
			Humanize:            cfg.Humanize,
			DisableWebgl:        cfg.DisableWebGL,
			HideWebdriver:       cfg.HideWebdriver,
			ViewportWidth:       protoconv.Int32Ptr(cfg.ViewportWidth),
			ViewportHeight:      protoconv.Int32Ptr(cfg.ViewportHeight),
			Locale:              cfg.Locale,
			UserAgent:           cfg.UserAgent,
			Proxy:               cfg.Proxy,
			Cookies:             cfg.Cookies,
			Headers:             headersToProto(cfg.Headers),
			CdpUrl:              cfg.CDPURL,
		}}
		if msg.Engine == webcontentpb.FetcherOptions_ENGINE_UNSPECIFIED {
			msg.Engine = webcontentpb.FetcherOptions_ENGINE_DYNAMIC
		}
	case StealthyConfig:
		if err := requireEngine(f.Engine, EngineStealthy); err != nil {
			return nil, err
		}
		msg.Config = &webcontentpb.FetcherOptions_Stealthy_{Stealthy: &webcontentpb.FetcherOptions_Stealthy{
			Headless:            cfg.Headless,
			BlockImages:         cfg.BlockImages,
			DisableResources:    cfg.DisableResources,
			GoogleSearch:        cfg.GoogleSearch,
			NetworkIdle:         cfg.NetworkIdle,
			WaitForSelector:     cfg.WaitForSelector,
			WaitSelectorState:   cfg.WaitSelectorState,
			WaitMilliseconds:    protoconv.DurationToMsPtr(cfg.Wait),
			TimeoutMilliseconds: protoconv.DurationToMsPtr(cfg.Timeout),
			Humanize:            cfg.Humanize,
			DisableWebgl:        cfg.DisableWebGL,
			ViewportWidth:       protoconv.Int32Ptr(cfg.ViewportWidth),
			ViewportHeight:      protoconv.Int32Ptr(cfg.ViewportHeight),
			Locale:              cfg.Locale,
			Timezone:            cfg.Timezone,
			OsFingerprint:       cfg.OSFingerprint,
			Proxy:               cfg.Proxy,
			Cookies:             cfg.Cookies,
			Headers:             headersToProto(cfg.Headers),
			SolveCloudflare:     cfg.SolveCloudflare,
		}}
		if msg.Engine == webcontentpb.FetcherOptions_ENGINE_UNSPECIFIED {
			msg.Engine = webcontentpb.FetcherOptions_ENGINE_STEALTHY
		}
	default:
		return nil, invalidArgument(fmt.Sprintf("unknown engine config type %T", f.Config))
	}
	return msg, nil
}

func requireEngine(set, want Engine) error {
	if set != EngineAuto && set != want {
		return invalidArgument(fmt.Sprintf("engine %q does not match config for engine %q", set, want))
	}
	return nil
}
