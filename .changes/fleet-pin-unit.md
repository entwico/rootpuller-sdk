---
bump: minor
---

`fleet.Service.PinUnit` and `UnpinUnit` keep a unit running for up to 12h, whatever its idle time, for using a worker such as ComfyUI directly; `Unit.PinnedUntil` and `Unit.PinWaitReason` show pins in the state. Needs rootpuller-api with fleet pins. `rootpullertest.Fleet` answers the pin calls (`PinFunc`, `UnpinFunc`).
