---
bump: patch
---

voice: `Save` no longer lets the server save a voice when a frame fails to send on the client side (a cancelled context, a name or sample name that is not valid UTF-8): a failed sample chunk aborts the call instead of closing it cleanly, so no slot is taken by a voice whose `Ref` would be lost, and a params frame that cannot be sent reports its own cause instead of the server's "missing params". `rootpullertest.Voice` now rejects what the server rejects: language codes other than lowercase ISO 639-1/639-3, whitespace-only texts and names, names over 100 characters, descriptions and texts over 40,000 characters, empty samples, samples over 64 MiB, previews outside 1-4, empty model overrides and speed or stability outside 0-1. `rootpullertest.Video` checks a talking video's speech the same way and rejects a blank model or prompt on both RPCs.
