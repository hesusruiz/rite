// Copyright 2023 Jesus Ruiz. All rights reserved.
// Use of this source code is governed by an Apache-2.0
// license that can be found in the LICENSE file.

// Package sliceedit extends the functionalities of rsc.io/edit to
// implement eficient buffered editing of byte slices.
// It requires a single allocation for many operations.
package sliceedit

import (
	"bytes"

	"rsc.io/edit"
)

// A Buffer is a queue of edits to apply to a given byte slice.
type Buffer struct {
	ed  edit.Buffer
	buf []byte
}

// NewBuffer returns a new buffer to accumulate changes to an initial data slice.
// The returned buffer maintains a reference to the data, so the caller must ensure
// the data is not modified until after the Buffer is done being used.
func NewBuffer(buf []byte) *Buffer {
	b := &Buffer{}
	b.buf = buf // Just for our internal queries, we do not modify anything in it
	b.ed = *edit.NewBuffer(buf)
	return b
}

// FindAll finds all non-overlapping instances of item in buf.
func FindAll(buf []byte, item string) []int {
	found := []int{}

	if len(item) == 0 {
		return found
	}

	realOffset := 0

	for {
		i := bytes.Index(buf, []byte(item))
		if i == -1 {
			return found
		}
		found = append(found, i+realOffset)
		buf = buf[i+len(item):]
		realOffset = realOffset + i + len(item)
	}
}

// Delete deletes the text s.
func (b *Buffer) DeleteAllString(s string) {
	hits := FindAll(b.buf, s)
	for _, hit := range hits {
		b.ed.Delete(hit, hit+len(s))
	}
}

// Replace replaces old with new.
func (b *Buffer) ReplaceAllString(old string, new string) {
	hits := FindAll(b.buf, old)
	for _, hit := range hits {
		b.ed.Replace(hit, hit+len(old), new)
	}
}

// Bytes returns a new byte slice containing the original data
// with the queued edits applied.
func (b *Buffer) Bytes() []byte {
	return b.ed.Bytes()
}

// String returns a string containing the original data
// with the queued edits applied.
func (b *Buffer) String() string {
	return string(b.ed.Bytes())
}
