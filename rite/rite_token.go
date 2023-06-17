// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rite

import (
	"bytes"
	"strconv"
)

// A TokenType is the type of a Token.
type TokenType uint32

const (
	// ErrorToken means that an error occurred during tokenization.
	ErrorToken TokenType = iota
	// TextToken means a text node.
	TextToken
	// A StartTagToken looks like <a>.
	StartTagToken
	// An EndTagToken looks like </a>.
	EndTagToken
	// A SelfClosingTagToken tag looks like <br/>.
	SelfClosingTagToken
	// A CommentToken looks like <!--x-->.
	CommentToken
	// A DoctypeToken looks like <!DOCTYPE x>
	DoctypeToken

	D2Token
	DiagramToken
	CodeToken
	PreToken
	HeaderToken
	ListToken
	SectionToken
	ParagraphToken
)

// String returns a string representation of the TokenType.
func (t TokenType) String() string {
	switch t {
	case ErrorToken:
		return "Error"
	case DoctypeToken:
		return "Doctype"
	case D2Token:
		return "D2"
	case DiagramToken:
		return "Diagram"
	case CodeToken:
		return "Code"
	case PreToken:
		return "Pre"
	case HeaderToken:
		return "List"
	case ListToken:
		return "Section"
	case SectionToken:
		return "Section"
	case ParagraphToken:
		return "Paragraph"
	}
	return "Invalid(" + strconv.Itoa(int(t)) + ")"
}

// A Token consists of a TokenType and some Data (tag name for start and end
// tags, content for text, comments and doctypes). A tag Token may also contain
// a slice of Attributes. Data is unescaped for all Tokens (it looks like "a<b"
// rather than "a&lt;b"). For tag Tokens, DataAtom is the atom for Data, or
// zero if Data is not a known tag name.
type Token struct {
	Type        TokenType
	Data        string
	Attr        []Attribute
	number      int
	indentation int
	Id          []byte
	Class       []byte
	Src         []byte
	Href        []byte
	Bucket      []byte
	Number      []byte
	StdFields   []byte
	RestLine    []byte
}

// tagString returns a string representation of a tag Token's Data and Attr.
func (t Token) tagString() string {
	buf := bytes.NewBufferString(t.Data)
	if t.Id != nil {
		buf.WriteByte(' ')
		buf.WriteString(`id="`)
		buf.Write(t.Id)
		buf.WriteString(`"`)
	}
	if t.Class != nil {
		buf.WriteByte(' ')
		buf.WriteString(`class="`)
		buf.Write(t.Class)
		buf.WriteString(`"`)
	}
	if t.Src != nil {
		buf.WriteByte(' ')
		buf.WriteString(`src="`)
		buf.Write(t.Src)
		buf.WriteString(`"`)
	}
	if t.Href != nil {
		buf.WriteByte(' ')
		buf.WriteString(`href="`)
		buf.Write(t.Href)
		buf.WriteString(`"`)
	}

	for _, a := range t.Attr {
		buf.WriteByte(' ')
		buf.WriteString(a.Key)
		buf.WriteString(`="`)
		escape(buf, a.Val)
		buf.WriteByte('"')
	}
	return buf.String()
}

// String returns a string representation of the Token.
func (t Token) String() string {
	switch t.Type {
	case ErrorToken:
		return ""
	case TextToken:
		return EscapeString(t.Data)
	case StartTagToken, DiagramToken, CodeToken, PreToken, HeaderToken, ListToken, SectionToken, ParagraphToken:
		return "<" + t.tagString() + ">"
	case EndTagToken:
		return "</" + t.tagString() + ">"
	case SelfClosingTagToken:
		return "<" + t.tagString() + "/>"
	case CommentToken:
		return "<!--" + escapeCommentString(t.Data) + "-->"
	case DoctypeToken:
		return "<!DOCTYPE " + EscapeString(t.Data) + ">"
	}
	return "Invalid(" + strconv.Itoa(int(t.Type)) + ")"
}
