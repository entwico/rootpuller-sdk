package streamio

// MaxChunkBytes is the upload chunk size: 2 MiB, matching the server's
// own chunking convention and staying comfortably under its 4 MiB gRPC
// receive limit.
const MaxChunkBytes = 2 << 20
