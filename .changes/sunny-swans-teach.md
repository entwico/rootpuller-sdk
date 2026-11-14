---
bump: minor
---

voice: choose the provider per call (voice.ProviderElevenLabs, voice.ProviderFishAudio; DesignOptions.Provider, SaveOptions.Provider), and the new Service.CloneSpeak speaks a text in a voice cloned from an uploaded sample without saving it. Breaking: GetCapabilities now takes the provider to describe (voice.ProviderDefault for the server's default); Capabilities reports Provider, AvailableProviders, CanCloneSpeak and the credit balance. rootpullertest.Voice gains CloneSpeakFunc and records the requested provider
