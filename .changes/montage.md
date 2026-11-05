---
bump: minor
---

New `montage` package for `MontageService`: `Render` turns a `Timeline` of video segments and stills (cuts or crossfades, kept, ducked or muted clip sound), audio tracks (voice-overs, looping music beds, fades) and image overlays (captions, logos) plus the assets it names into one MP4, with progress and the result's duration and size; `GetCapabilities` reports the server's limits. `rootpullertest.Montage` fakes it, enforcing the wire protocol and the timeline rules that need no media. See docs/montage.md.
