package rootpullersdk

// ImageFormat selects the encoding of an output image. The zero value
// leaves the choice to the server.
type ImageFormat string

const (
	ImageFormatUnspecified ImageFormat = ""
	ImageFormatJPG         ImageFormat = "jpg"
	ImageFormatPNG         ImageFormat = "png"
	ImageFormatWebP        ImageFormat = "webp"
)

// Point is a 2D coordinate in pixels.
type Point struct {
	X float32
	Y float32
}

// ImageSize is a width/height pair in pixels.
type ImageSize struct {
	Width  int
	Height int
}

// BoundingBox is an axis-aligned rectangle anchored at Position.
type BoundingBox struct {
	Position Point
	Size     ImageSize
}
