---
bump: minor
---

`painter.Service.RefineFaces` redraws small faces inside an image at high resolution on the comfy backend (`RefineFacesComfy`, Qwen) and blends them back, leaving the rest of the image as it was (bit-identical for 8-bit PNG). References carry the identity, `SearchRegions` or `FaceBoxes` choose the faces, and `Metadata.Faces` reports the refined boxes. `rootpullertest.Painter` gains `RefineFacesFunc`.
