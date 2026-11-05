---
bump: minor
---

voice: `SpeakOptions.Format` picks the encoding of the speech (MP3 or lossless WAV, at the sample rates and bit rates in `Capabilities.AudioFormats`); `Speech.Format` reports what was delivered. montage: `Output.AudioBitrate` overrides the quality's AAC bit rate, `Metadata` reports the delivered audio (sample rate, channels, bit rate), and the quality levels are documented. The fakes record and enforce both.
