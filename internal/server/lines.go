package server

import (
	"bytes"
	"io"
)

// newLineReader lets encoding/json stream a JSONL file: the decoder happily
// reads one value after another from a plain reader, so the only thing needed is
// the reader itself. It exists as a named function so the reason is written down
// somewhere rather than being an inline bytes.NewReader nobody dares touch.
func newLineReader(data []byte) io.Reader { return bytes.NewReader(data) }
