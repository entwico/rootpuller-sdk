// Package common holds facade types shared by several rootpuller-sdk
// service packages: file payloads, image geometry, text chunks, and usage
// accounting. They mirror the com.entwico.rootpuller.common proto package.
package common

import (
	"bytes"
	"io"
)

// File is a fully buffered binary payload returned by (or passed to)
// image-processing RPCs.
type File struct {
	Name     string
	MIMEType string
	Data     []byte
}

// Upload is a streamed binary input. Content is read sequentially and
// sent to the server in chunks; it is not seeked or reread.
type Upload struct {
	Name     string
	MIMEType string
	// Size is the total content length in bytes when known, 0 otherwise.
	// The server does not require it.
	Size    int64
	Content io.Reader
}

// UploadBytes wraps an in-memory payload as an Upload.
func UploadBytes(name, mimeType string, data []byte) Upload {
	return Upload{Name: name, MIMEType: mimeType, Size: int64(len(data)), Content: bytes.NewReader(data)}
}

// AsUpload converts a buffered File into an Upload.
func (f File) AsUpload() Upload {
	return UploadBytes(f.Name, f.MIMEType, f.Data)
}
