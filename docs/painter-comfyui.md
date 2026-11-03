# Self-hosted image models with the painter package

The painter service has three backends: Google, OpenAI, and **comfy**. The
comfy backend runs open-weight image models on our own GPU, behind
rootpuller-api. You send a prompt, optional reference images and a few
options. You never see the ComfyUI behind it: there are no workflows, node IDs
or file names in the API.

You need `github.com/entwico/rootpuller-sdk` v0.7.0 or later and rootpuller-api
v3.4.0 or later.

## Which model for what

| Model | Constant | Input | Use it for | Default size | Speed on our GPU |
|---|---|---|---|---|---|
| `qwen-image-2.1` | `painter.ComfyModelQwenImage` | text, up to 7 reference images; **edit** | putting a real product, person or logo into a new scene; combining several references; editing an existing image (change the text on a sign, swap an object, change the season) | 1152×1536 | about 30 s at 768×1024 (40 steps); full size takes longer |
| `moss-image` | `painter.ComfyModelMOSSImage` | text only | photorealistic scenes from a description, especially with **readable text** in the picture: menus, signs, labels, posters | 1152×1536 | about 2 min at full size (50 steps) |
| `krea-2-turbo` | `painter.ComfyModelKrea2Turbo` | text only | quick drafts to explore an idea or a composition before rendering it with one of the other models | 1536×1024 | the fastest of the three (8 steps) |

Rules of thumb:

- **Something that has to look like a real thing you have a picture of?** Use
  Qwen with references.
- **Changing an image you already have?** Use Qwen through `Edit`.
- **A new picture from words only?** Use MOSS. Try Krea first if you are
  still trying out prompts.
- **Need a transparent background, a mask-based edit, or outpainting?** Use
  the Google or OpenAI backends. The comfy backend does not do these.

The timings were measured on production with one image while the GPU was
free. Jobs run one after
another on a single GPU, so a busy deployment adds waiting time (see
[Queueing and progress](#queueing-and-progress)). To try out prompts, render
small (e.g. 768×1024) and only render full size once the prompt works.

## Setup

The comfy backend uses the same client as the rest of the SDK:

```go
sdk, err := rootpullersdk.New("http://rootpuller-api:8755",
    rootpullersdk.WithTokenSource(oauth.TokenSource(ctx)),
)
if err != nil {
    return err
}

p := painter.NewService(sdk)
```

### Picking a deployment

rootpuller-api can route to several ComfyUI deployments. Each one has a name
and a list of models it serves. The production deployment is called
`cluster`, serves all three models, and is the default. You only need to pick
a deployment when there is more than one:

```go
// for every comfy call made by this service
p := painter.NewService(sdk, painter.WithComfyUIDeployment("cluster"))

// for a single call; overrides the service default
ctx = rootpullersdk.ContextWithComfyUIDeployment(ctx, "cluster")
```

Either one sets the `comfyui-deployment` gRPC header. Without it the server
uses its configured default deployment.

## Generate from text

```go
res, err := p.Generate(ctx,
    `Candid smartphone photo of a chalkboard menu leaning against the wall of a
wooden mountain hut, handwritten in white chalk: "Kaiserschmarrn 12,50" and
"Apfelstrudel 6,90". Flat overcast daylight, a bit of clutter.`,
    &painter.GenerateOptions{
        Backend: painter.GenerateComfy{
            Model:  painter.ComfyModelMOSSImage,
            Width:  rootpullersdk.Ptr(1152),
            Height: rootpullersdk.Ptr(1536),
        },
    })
if err != nil {
    return err
}

for _, img := range res.Images {
    // img.Name is "image_0.png", "image_1.png", …; img.Data holds the bytes
    _ = os.WriteFile(img.Name, img.Data, 0o644)
}
```

`GenerateComfy` options:

| Field | Meaning | Default |
|---|---|---|
| `Model` | required, one of the constants above | — |
| `NumberOfImages` | 1–4. Each image is a separate GPU job, run one after another | 1 |
| `Seed` | seed of the first image; image *i* uses `Seed + i`. A fixed seed with the same prompt and size gives the same picture again | random |
| `Width`, `Height` | multiples of 16, 256–2048 each | per model, see the table |
| `OutputFormat` | `ComfyOutputFormatPNG` or `ComfyOutputFormatJPEG` (quality 92) | PNG |

Useful sizes: portrait 1152×1536, landscape 1536×1152, square 1328×1328,
story 1088×1920.

## Generate with reference images (Qwen)

References are images the model should reproduce: a product, a logo, a
person, a room. The prompt refers to them as `<image1>`, `<image2>`, … in the
order of `References`:

```go
bottle, _ := os.Open("bottle.jpg")
label, _ := os.Open("label-closeup.png")

res, err := p.Generate(ctx,
    `Candid documentary photo of the bottle from <image1> lying in autumn
leaves on a forest floor, the label exactly as shown in <image2>. Soft
overcast light, reflections of the environment on the glass.`,
    &painter.GenerateOptions{
        Backend: painter.GenerateComfy{Model: painter.ComfyModelQwenImage},
        References: []rootpullersdk.Upload{
            {Name: "bottle.jpg", MIMEType: "image/jpeg", Content: bottle},
            {Name: "label.png", MIMEType: "image/png", Content: label},
        },
    })
```

- Up to 7 references, each at most 32 MiB, as PNG, JPEG or WebP.
- Only Qwen accepts references. With MOSS or Krea the call fails with
  `InvalidArgument`. With the Google or OpenAI backend the SDK rejects it
  locally.
- Name the references explicitly in the prompt ("the bottle from `<image1>`")
  and say what has to stay unchanged ("the label exactly as shown in
  `<image2>`"). A tight crop of the detail that matters (a label, a face, a
  logo) works better than a wide shot where that detail is small.

## Edit an existing image (Qwen)

`Edit` keeps the layout of the input image and changes what the prompt asks
for. The input image is also `<image1>` in the prompt:

```go
in, _ := os.Open("chalkboard.png")

res, err := p.Edit(ctx,
    `Change only the chalk text on the board in <image1> to "Apfelstrudel 6,90".
Keep everything else exactly the same.`,
    rootpullersdk.Upload{Name: "chalkboard.png", MIMEType: "image/png", Content: in},
    &painter.EditOptions{
        Backend: painter.EditComfy{Model: painter.ComfyModelQwenImage},
    })
```

- Only Qwen supports `Edit`. MOSS and Krea fail with `InvalidArgument`.
- The output keeps the input's size unless you set `Width` and `Height`. If you
  set them, the input is scaled and centre-cropped to that size first.
- `EditOptions.Mask` is not supported on the comfy backend. To restrict the
  change, describe it in the prompt and say what must stay the same.
- `OutpaintImage` has no comfy backend.

## Queueing and progress

A ComfyUI deployment runs one job at a time. Jobs from all callers wait in its
queue. The call stays open while it waits, with no time limit on the wait
itself, and reports what is happening through `OnProgress`:

```go
opts := &painter.GenerateOptions{
    Backend: painter.GenerateComfy{Model: painter.ComfyModelMOSSImage},
    OnProgress: func(pr painter.Progress) {
        log.Debug("painter progress", "stage", pr.Stage, "pct", pr.Percentage, "msg", pr.Message)
    },
}
```

| Stage | Message, for example |
|---|---|
| `StageQueued` | `queued: 2 job(s) ahead` (repeated while waiting) |
| `StageGenerating` | `generating image 1 of 2: step 17/50` |
| `StageUploading` | the images are being sent back to you |

`Percentage` goes from 0 to 1 and never decreases. With several images it
covers all of them.

**Timeouts and cancelling.** Set the deadline on your `ctx` to how long you
are willing to wait, including the queue. Cancelling the context, or reaching
its deadline, removes your jobs from the queue or stops the one that is
running, so an abandoned call does not keep the GPU busy. Independently of
your deadline, the server gives up after 20 minutes per image and returns
`Unavailable`.

## What comes back

`Result.Images` holds the files in order (`image_0.png`, `image_1.png`, …).
`Result.Metadata` holds:

- `Backend` is `"comfy"`, and `Model` is the model you asked for.
- `Images[i].Seed` is the seed actually used. Keep it if you want to render the
  same picture again, for example at a larger size.
- `Images[i].Size` is the output size.
- `Notes` contains `deployment: <name>`, which tells you where the job ran.
- `ProcessingTime` is the wall time including the queue.
- `Usage` is empty. Self-hosted calls have no token or cost accounting.

The images are re-encoded by rootpuller-api before they reach you. ComfyUI
writes its whole workflow into the PNG metadata, and that is removed. PNG
output is lossless, so the pixels are exactly what the model produced.

There is no safety filter on the comfy backend. `SafetyBlocked` is always
false, so a call never comes back with a blocked image.

## Errors

| Code | When |
|---|---|
| `InvalidArgument` | missing `Model`; references with a text-only model or more than 7; `Edit` with MOSS or Krea; a mask with `EditComfy`; a size that is not a multiple of 16 or outside 256–2048; `NumberOfImages` outside 1–4; an input that is not a PNG, JPEG or WebP image |
| `NotFound` | the deployment named in the header is not configured, or that deployment does not serve the model. The message lists the models it does serve |
| `Unavailable` | the comfy backend is not configured on this rootpuller-api, the deployment cannot be reached, or a job ran longer than 20 minutes per image |
| `Canceled`, `DeadlineExceeded` | your context ended. The jobs were removed from the deployment |

## Testing your code

`rootpullertest` fakes the painter service. `GenerateRequestFunc` gives you the
decoded request, including the reference images in order and the
`comfyui-deployment` header, so you can assert on them without a GPU.

## Trying it in the showroom

The showroom (Painter → Generate image / Edit image) has the comfy backend
with templates for each model: `comfy moss chalkboard`, `comfy qwen
references`, `comfy krea draft`, and `comfy qwen translate` for edit. The
Routing menu has an `X-ComfyUI-Deployment` header for picking a deployment.
Each request's JSON and curl view show what the SDK sends.
