# Assembling videos with the montage package

The montage service is the last step of a production. The pieces are made
elsewhere: clips with `video`, voice-overs with `voice`, music with
`composer`, stills and caption images with `painter` or your own renderer.
`Render` cuts them together, mixes the sound, puts captions on top and
encodes one MP4 (H.264 + AAC) that plays everywhere social.

You need the rootpuller-api release that adds `MontageService` (after
v3.6.0).

## The flow

```go
svc := montage.NewService(sdk)

timeline := montage.Timeline{
    Segments: []montage.Segment{
        // ambience under the voice-over, then a half-second crossfade
        {Asset: "shot1", Sound: montage.SoundDuck, GainDB: new(-18.0),
            Transition: montage.Transition{Kind: montage.TransitionCrossfade, Duration: 500 * time.Millisecond}},
        {Asset: "shot2", Sound: montage.SoundDuck, GainDB: new(-18.0)},
        {Asset: "shot3", Start: time.Second, Duration: 4 * time.Second, Sound: montage.SoundMute},
        // a still closes the reel
        {Asset: "cover", Duration: 2 * time.Second},
    },
    AudioTracks: []montage.AudioTrack{
        {Asset: "voice", At: 300 * time.Millisecond},
        {Asset: "music", GainDB: -22, Loop: true, FadeOut: 2 * time.Second},
    },
    Overlays: []montage.Overlay{
        {Asset: "caption", At: time.Second, Duration: 3 * time.Second,
            Anchor: montage.AnchorBottom, MarginY: 160, FadeIn: 200 * time.Millisecond},
    },
    Output:       montage.Output{FadeOut: 500 * time.Millisecond},
    DurationMode: montage.DurationAudio, // the reel ends with the voice-over
}

res, err := svc.Render(ctx, timeline, []montage.Asset{
    {ID: "shot1", Upload: clip1.Video.AsUpload()},   // video.Result
    {ID: "shot2", Upload: clip2.Video.AsUpload()},
    {ID: "shot3", Upload: clip3.Video.AsUpload()},
    {ID: "cover", Upload: cover.AsUpload()},         // a painter image
    {ID: "voice", Upload: speech.Audio.AsUpload()},  // voice.Speech
    {ID: "music", Upload: song.Audio.AsUpload()},
    {ID: "caption", Upload: rootpullersdk.UploadBytes("caption.png", "image/png", captionPNG)},
}, &montage.RenderOptions{OnProgress: func(p montage.Progress) { log.Println(p.Stage, p.Percentage) }})

// res.Video is the MP4; res.Metadata.Duration is its exact length.
```

Asset ids are yours to choose. Every id the timeline names must be passed
once, and nothing else; `montage.ReferencedAssets(timeline)` lists them. One
asset can be used in several places.

## The model

- **One video track.** Segments play in order. A segment is a video (trim it
  with `Start` and `Duration`) or a still image (`Duration` is required).
- **A segment's own sound** is kept, ducked by `GainDB` (required with
  `SoundDuck`, rejected without it), or muted. Stills are silent.
- **Crossfades shorten the video.** The two segments overlap by the
  crossfade's length: three 4 s clips joined by two 500 ms crossfades make
  11 s, not 12. A crossfade must be shorter than the segments it joins.
  Segment lengths are rounded to whole output frames.
- **Audio tracks** sit on the output's clock (`At`), trimmed, with gain and
  fades. The asset can be an audio file or a video whose sound is used.
  `Loop` repeats a short music bed until the end; its fades apply once, to the
  whole looped track.
- **Overlays** are images with transparency, pinned to an anchor with
  margins, for a time range. Captions and titles are images you render: no
  fonts or text layout are part of this API.
- **The output's length** comes from the video track (default), from the last
  audio track that does not loop (`DurationAudio`: the voice-over decides,
  and a shorter video holds its last frame), or is set (`DurationExplicit`
  with `Timeline.Duration`).
- **Size** defaults to the first segment's display size and is required when
  that segment is an image; **rate** defaults to the first video segment's, or
  30 fps for stills only. Segments of a different size are scaled; a
  different aspect ratio (more than 1% off) is an error, so render every piece
  in the target shape (576×1024 for 9:16 reels from LTX). Rotated phone clips
  count upright.

## Errors

A timeline the server cannot render fails with `InvalidArgument` and a
message that says what to fix: an asset id that does not match, a trim past a
clip's end, a crossfade longer than a segment, an aspect ratio that differs
from the output's, an asset that is not media of the kind its place needs, an
output longer than `GetCapabilities` allows. Those checks run before the
render starts.

`GetCapabilities` reports the most assets per render, the size of one asset
and of all together, the most segments, audio tracks and overlays, the
largest output dimension and the longest output, plus the accepted media
types.

A render that waits for a free server slot reports `StageQueued`; it is never
rejected for being busy.

## Testing

`rootpullertest.Montage` fakes the service. `RenderFunc` sees the timeline
and every asset's bytes; it enforces the wire protocol, the limits it
advertises, and the timeline rules that need no media (ids, gains, crossfade
lengths, duration modes, output size, empty assets), not the ones that do
(trims past a clip's end, aspect ratios).
