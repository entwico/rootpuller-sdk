---
bump: patch
---

A streaming upload that fails part-way (its reader returns an error, or a frame cannot be sent) now cancels the call instead of ending it cleanly. Before, the server received a well-formed but truncated upload and processed it: a talking-head video could start rendering from half a voice track. This covers the uploads that stream params, then files, then read results: painter (`GenerateImage`, `EditImage`, `OutpaintImage`, `RefineFaces`), video (`Generate`, `GenerateTalking`, `GenerateTalkingSpeech`), assetia (`TransformImage`, `TransformVideo`, `GeneratePreview`), `bgremover.RemoveBackground`, `unshakaler.UpscaleImage`, facefixer (`RestoreFace`, `ColorizeFace`, `InpaintFace`), `webcontent.ExtractContent`, `composer.Transcribe`, `escriba.TranscribeRecording`, vectorops (`ClusterHdbscan`, `ProjectUmap`) and `montage.Render`. `assetia.ProbeMedia`, `escriba.Transcribe` and `completion.CompleteWithAttachments` are not covered yet.
