# Synthetic voices with the voice package

The voice service turns a written description of a voice into a real voice
you can keep and use. You design previews, save the one you like, and then
speak any text in it. `CloneSpeak` speaks a text in a voice cloned from a
sample you upload, without saving anything. A talking-head video can speak a
text in a saved voice directly.

## Providers

rootpuller-api can serve two providers, each enabled by its API key in the
server's configuration: `voice.ProviderElevenLabs` and
`voice.ProviderFishAudio`. One of them is the server's default. A call picks
a provider through `DesignOptions.Provider`, `SaveOptions.Provider`,
`CloneSpeakOptions.Provider` or `GetCapabilities`' argument;
`voice.ProviderDefault` (the zero value) is the server's default.
`GetCapabilities(...).AvailableProviders` lists what the server has
configured, its default first.

A `Ref` and a preview `Handle` remember the provider that made them, so
`Speak`, `Delete` and `Save` with a handle need no provider.

| | ElevenLabs | Fish Audio |
|---|---|---|
| Design | previews as MP3 | previews as WAV; the handle lives in the server's memory for an hour |
| Save | slots per plan | voice models, no slots; the account needs a model quota (it comes with topped-up API credit) |
| CloneSpeak | emulated: a temporary voice is saved, used and deleted, so it takes a slot and a voice addition while it runs | inline: nothing is saved |
| Billing | characters (`CharactersUsed`, `CharactersLimit`) | UTF-8 bytes of text from a prepaid credit (`CreditBalanceMicros`) |
| Speed | 0 → 0.7×, 0.5 → 1×, 1 → 1.2× | 0 → 0.5×, 0.5 → 1×, 1 → 2× |
| Stability | the voice setting | maps to temperature: 0 → 1.0, 1 → 0.1 |

You need the SDK release after v0.14.0 and the rootpuller-api release after
v3.10.0 for providers and `CloneSpeak`.

## The flow

```go
svc := voice.NewService(sdk)

// 1. design: three previews of the voice saying the sample text.
design, err := svc.Design(ctx,
    "speaking German, a woman, early thirties, warm mid-range voice, "+
        "slight Rhineland accent, calm and friendly",
    "Hallo, ich bin Lena. Heute zeige ich euch, wie ich meinen Morgen plane – "+
        "mit Kaffee, einer Liste und ein bisschen Musik.",
    "de", nil)

// 2. listen to design.Previews[i].Audio, pick one, save it. Pass its audio
// too: if the handle has expired by now, the voice is cloned from it.
p := design.Previews[1]
sample := p.Audio.AsUpload()
saved, err := svc.Save(ctx, "Lena", &voice.SaveOptions{
    Description: "warm, calm, slight Rhineland accent",
    Handle:      p.Handle,
    Sample:      &sample,
})

// 3. store BOTH saved.Ref and p.Audio.Data with the avatar.

// 4. speak.
speech, err := svc.Speak(ctx, saved.Ref, "Guten Morgen zusammen!", "de", nil)
// speech.Audio is an MP3 (audio/mpeg); speech.Usage.Characters is what it cost.
```

Write the description in English, whatever the voice's language. Put the
language into the description as well ("speaking German"), and write the sample
text in that language: the provider takes the language from the text, so an
English sample gives an English-accented voice. The lengths the provider accepts
are in `GetCapabilities` (`DescriptionLimits`, `SampleTextLimits`); with
ElevenLabs a description takes 20–1000 characters and a sample text 100–1000.

## Cloning without saving

```go
sample := rootpullersdk.UploadBytes("me.wav", "audio/wav", recording)

speech, err := svc.CloneSpeak(ctx, &sample, "Guten Morgen zusammen!", "de", &voice.CloneSpeakOptions{
    Provider:   voice.ProviderFishAudio,
    SampleText: "what the recording says, word for word", // optional; clones more faithfully
})
```

10–30 seconds of clean speech work best. `CloneSpeakOptions` embeds
`SpeakOptions`, so model, speed, stability and format work as for `Speak`.
Clone only voices you have the rights to.

## Audio format

`Speak` returns MP3 at 44.1 kHz and 128 kbps unless `SpeakOptions.Format`
asks for another encoding. `GetCapabilities(ctx, provider).AudioFormats` lists what the
provider delivers, the default first; pick one from it:

```go
wav := voice.AudioFormat{Codec: voice.CodecWAV, SampleRate: 44100}
speech, err := svc.Speak(ctx, saved.Ref, text, "de", &voice.SpeakOptions{Format: &wav})
// speech.Audio is "speech_0.wav" (audio/wav); speech.Format reports what was
// delivered, Channels included (1: ElevenLabs speaks mono).
```

- `CodecWAV` is lossless 16-bit PCM, about 5 MB per minute at 44.1 kHz. Use it
  when the speech is mixed into a montage: the montage then encodes it once,
  at the output, instead of decoding an MP3 and encoding it again.
- Formats are matched on codec, sample rate and bit rate; one not in the list
  fails with `InvalidArgument`. With ElevenLabs, 192 kbps MP3 needs the
  Creator plan and 44.1 kHz WAV the Pro plan. The list leaves out what the
  account's plan does not include; a format the provider refuses anyway fails
  with `PermissionDenied`. Neither is worth retrying.
- `Design` previews are always MP3: they are for listening and picking.

## What to store

| Keep | Why |
|---|---|
| `Ref` | The voice at the server's current provider. Opaque: store it as a string and pass it back as-is. |
| The sample audio (the preview's `Audio`, or what you cloned from) | The voice's real identity. It re-creates the voice when the provider changes. |

Do not treat the `Ref` as the voice. It is a cache of the voice at one
provider.

## When a provider goes away

A `Ref` saved with a provider the server no longer has configured cannot be
spoken: no other provider knows its voice id. `Speak` fails with
`FailedPrecondition`, never with some other voice, and `voice.IsProviderMismatch`
recognises the error. Save the stored sample again and replace the `Ref`:

```go
speech, err := svc.Speak(ctx, ref, text, "de", nil)
if voice.IsProviderMismatch(err) {
    sample := rootpullersdk.UploadBytes("sample.mp3", "audio/mpeg", storedSample)

    saved, serr := svc.Save(ctx, name, &voice.SaveOptions{Sample: &sample})
    if serr != nil {
        return serr
    }

    ref = saved.Ref // store it
    speech, err = svc.Speak(ctx, ref, text, "de", nil)
}
```

`GenerateTalkingSpeech` fails the same way and the same check applies.

## Talking-head videos

`video.Service.GenerateTalkingSpeech` takes the text and the voice instead of a
voice track; the server speaks the text first, then lip-syncs the portrait:

```go
res, err := videoSvc.GenerateTalkingSpeech(ctx,
    "A friendly woman talks to the camera in a bright kitchen, static camera.",
    portrait,
    video.Speech{Voice: saved.Ref, Text: "Guten Morgen zusammen!", Language: "de"},
    nil)
```

Progress starts with `video.StageSpeaking`. To reuse the audio, or to check it
before a long render, call `Speak` yourself and pass `speech.Audio.AsUpload()`
to `GenerateTalking`.

## Limits and costs

- `GetCapabilities` reports what a provider allows: whether designing,
  cloning and `CloneSpeak` work, the saved-voice slots in use and their limit, the speech and
  design models with their languages and each model's longest text
  (`Model.MaxTextChars`), the text limits of `Design`, and how much of a `Save`
  sample is used (`MaxSampleSeconds`), and the formats `Speak` delivers
  (`AudioFormats`). Check it to show "not available" before a call fails.
- Leave `SpeakOptions.Speed` nil for the voice's natural pace; 0 is the
  slowest, not the default.
- With ElevenLabs every saved voice takes a slot (10 on the current plan).
  `Delete` frees one; delete voices you no longer need.
- ElevenLabs bills designs and speech per character, Fish Audio speech per
  UTF-8 byte and a design per request. `Usage.Characters` is the text's
  length in characters, which is what ElevenLabs bills; Fish Audio bills the
  UTF-8 bytes, more for non-ASCII text. `EstimatedCostMicros` is an estimate
  in each provider's own unit.
- Out of characters, slots or concurrent requests fails with
  `ResourceExhausted`; an operation the plan does not include fails with
  `PermissionDenied`.
- Preview handles expire. `Preview.ExpiresAt` says when, if the provider
  tells: ElevenLabs does not (nil), Fish Audio handles last an hour. A Fish
  Audio handle lives in one server process's memory, so on another replica or
  after a restart it counts as expired. Save soon after designing, and always
  pass the preview's audio as the sample too: an expired handle, or one from
  a provider the server does not have configured, then falls back to cloning
  it.

## Testing

`rootpullertest.Voice` fakes the service. Its hooks see the requests,
providers and `VoiceSpeakRequest.Format` included; `CloneSpeakFunc` sees the
whole sample. By default it reports ElevenLabs (the default) and Fish Audio
as available; a provider missing from `Capabilities.AvailableProviders` fails
with `Unavailable`. It accepts only the formats it reports
(by default MP3 at 128 and 192 kbps and WAV at 44.1 and 48 kHz) and answers a
WAV request with "speech_0.wav". Return
`rootpullertest.VoiceProviderMismatch()` from `SpeakFunc` to test the migration
path.
