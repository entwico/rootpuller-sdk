# Synthetic voices with the voice package

The voice service turns a written description of a voice into a real voice
you can keep and use. You design previews, save the one you like, and then
speak any text in it. A talking-head video can speak a text in a saved voice
directly.

The provider behind it (ElevenLabs today) is rootpuller-api's configuration.
This package never names it, and switching providers later changes no client
code. You need the SDK release after v0.9.0 and the rootpuller-api release
after v3.5.0.

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

## Audio format

`Speak` returns MP3 at 44.1 kHz and 128 kbps unless `SpeakOptions.Format`
asks for another encoding. `GetCapabilities().AudioFormats` lists what the
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

## When the provider changes

A `Ref` saved with a provider the server no longer uses cannot be spoken: the
new provider does not know its voice id. `Speak` fails with
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

- `GetCapabilities` reports what the provider allows: whether designing and
  cloning work, the saved-voice slots in use and their limit, the speech and
  design models with their languages and each model's longest text
  (`Model.MaxTextChars`), the text limits of `Design`, and how much of a `Save`
  sample is used (`MaxSampleSeconds`), and the formats `Speak` delivers
  (`AudioFormats`). Check it to show "not available" before a call fails.
- Leave `SpeakOptions.Speed` nil for the voice's natural pace; 0 is the
  slowest, not the default.
- Every saved voice takes a slot (10 on the current plan). `Delete` frees one;
  delete voices you no longer need.
- Designs and speech are billed per character. `Usage.Characters` on each
  result is what the provider billed; `EstimatedCostMicros` is an estimate.
- Out of characters, slots or concurrent requests fails with
  `ResourceExhausted`; an operation the plan does not include fails with
  `PermissionDenied`.
- Preview handles expire. `Preview.ExpiresAt` says when, if the provider
  tells (ElevenLabs does not, so it is nil today). Save soon after designing,
  and always pass the preview's audio as the sample too: an expired handle, or
  one from a provider the server no longer uses, then falls back to cloning
  it.

## Testing

`rootpullertest.Voice` fakes the service. Its hooks see the requests,
`VoiceSpeakRequest.Format` included; it accepts only the formats it reports
(by default MP3 at 128 and 192 kbps and WAV at 44.1 and 48 kHz) and answers a
WAV request with "speech_0.wav". Return
`rootpullertest.VoiceProviderMismatch()` from `SpeakFunc` to test the migration
path.
