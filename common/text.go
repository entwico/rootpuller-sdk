package common

// TextChunk is one chunk produced by the text chunking or document
// processing services. Indexes are rune offsets into the source text.
type TextChunk struct {
	Text       string
	StartIndex int
	EndIndex   int
	TokenCount int
}

// NormalizeOptions controls the optional text normalization pass applied
// before chunking. Nil fields keep the server default (off).
type NormalizeOptions struct {
	NormalizeWhitespace *bool
	NoLineBreaks        *bool
	FixUnicode          *bool
	ToASCII             *bool
	Lower               *bool
	NoURLs              *bool
	NoEmails            *bool
	NoPhoneNumbers      *bool
	NoNumbers           *bool
	NoCurrencySymbols   *bool
	NoPunctuation       *bool
	NoEmoji             *bool
}
