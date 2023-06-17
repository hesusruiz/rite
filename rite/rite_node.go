// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rite

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// A NodeType is the type of a Node.
type NodeType uint32

const (
	ErrorNode NodeType = iota
	DocumentNode
	DivNode
	D2Node
	DiagramNode
	CodeNode
	PreNode
	HeaderNode
	ListNode
	SectionNode
	ParagraphNode
	ExplanationsNode
)

// String returns a string representation of the TokenType.
func (n NodeType) String() string {
	switch n {
	case ErrorNode:
		return "Error"
	case DocumentNode:
		return "Document"
	case DivNode:
		return "Div"
	case D2Node:
		return "D2"
	case DiagramNode:
		return "Diagram"
	case CodeNode:
		return "Code"
	case PreNode:
		return "Pre"
	case HeaderNode:
		return "List"
	case ListNode:
		return "Section"
	case SectionNode:
		return "Section"
	case ParagraphNode:
		return "Paragraph"
	case ExplanationsNode:
		return "Explanations"
	}
	return "Invalid(" + strconv.Itoa(int(n)) + ")"
}

// A Node consists of a NodeType and some Data (tag name for element nodes,
// content for text) and are part of a tree of Nodes. Element nodes may also
// have a Namespace and contain a slice of Attributes. Data is unescaped, so
// that it looks like "a<b" rather than "a&lt;b". For element nodes, DataAtom
// is the atom for Data, or zero if Data is not a known tag name.
type Node struct {
	Parent, FirstChild, LastChild, PrevSibling, NextSibling *Node

	Type NodeType
	Para *Text
	// Token       *Token
	Indentation int
	LineNumber  int
	Data        string
	Namespace   string
	Attr        []Attribute

	number    int
	Id        []byte
	Class     []byte
	Src       []byte
	Href      []byte
	Bucket    []byte
	Number    []byte
	StdFields []byte
	RestLine  []byte
}

// tagString returns a string representation of a tag Token's Data and Attr.
func (n Node) tagString() string {
	buf := bytes.NewBufferString(n.Data)
	if n.Id != nil {
		buf.WriteByte(' ')
		buf.WriteString(`id="`)
		buf.Write(n.Id)
		buf.WriteString(`"`)
	}
	if n.Class != nil {
		buf.WriteByte(' ')
		buf.WriteString(`class="`)
		buf.Write(n.Class)
		buf.WriteString(`"`)
	}
	if n.Src != nil {
		buf.WriteByte(' ')
		buf.WriteString(`src="`)
		buf.Write(n.Src)
		buf.WriteString(`"`)
	}
	if n.Href != nil {
		buf.WriteByte(' ')
		buf.WriteString(`href="`)
		buf.Write(n.Href)
		buf.WriteString(`"`)
	}

	for _, a := range n.Attr {
		buf.WriteByte(' ')
		buf.WriteString(a.Key)
		buf.WriteString(`="`)
		escape(buf, a.Val)
		buf.WriteByte('"')
	}
	return buf.String()
}

// String returns a string representation of the Token.
func (n Node) String() string {
	switch n.Type {
	case ErrorNode:
		return ""
	case DiagramNode, CodeNode, PreNode, HeaderNode, ListNode, SectionNode, ExplanationsNode:
		return "<" + n.tagString() + ">"
	case ParagraphNode:
		return "<p>"
	case DocumentNode:
		return "TopLevelDocument"
	}
	return "Invalid(" + strconv.Itoa(int(n.Type)) + ")"
}

// InsertBefore inserts newChild as a child of n, immediately before oldChild
// in the sequence of n's children. oldChild may be nil, in which case newChild
// is appended to the end of n's children.
//
// It will panic if newChild already has a parent or siblings.
func (n *Node) InsertBefore(newChild, oldChild *Node) {
	if newChild.Parent != nil || newChild.PrevSibling != nil || newChild.NextSibling != nil {
		panic("InsertBefore called for an attached child Node")
	}
	var prev, next *Node
	if oldChild != nil {
		prev, next = oldChild.PrevSibling, oldChild
	} else {
		prev = n.LastChild
	}
	if prev != nil {
		prev.NextSibling = newChild
	} else {
		n.FirstChild = newChild
	}
	if next != nil {
		next.PrevSibling = newChild
	} else {
		n.LastChild = newChild
	}
	newChild.Parent = n
	newChild.PrevSibling = prev
	newChild.NextSibling = next
}

// AppendChild adds a node c as a child of n.
//
// It will panic if c already has a parent or siblings.
func (n *Node) AppendChild(c *Node) {
	if c.Parent != nil || c.PrevSibling != nil || c.NextSibling != nil {
		panic("AppendChild called for an attached child Node")
	}
	last := n.LastChild
	if last != nil {
		last.NextSibling = c
	} else {
		n.FirstChild = c
	}
	n.LastChild = c
	c.Parent = n
	c.PrevSibling = last
}

// RemoveChild removes a node c that is a child of n. Afterwards, c will have
// no parent and no siblings.
//
// It will panic if c's parent is not n.
func (n *Node) RemoveChild(c *Node) {
	if c.Parent != n {
		panic("RemoveChild called for a non-child Node")
	}
	if n.FirstChild == c {
		n.FirstChild = c.NextSibling
	}
	if c.NextSibling != nil {
		c.NextSibling.PrevSibling = c.PrevSibling
	}
	if n.LastChild == c {
		n.LastChild = c.PrevSibling
	}
	if c.PrevSibling != nil {
		c.PrevSibling.NextSibling = c.NextSibling
	}
	c.Parent = nil
	c.PrevSibling = nil
	c.NextSibling = nil
}

// reparentChildren reparents all of src's child nodes to dst.
func reparentChildren(dst, src *Node) {
	for {
		child := src.FirstChild
		if child == nil {
			break
		}
		src.RemoveChild(child)
		dst.AppendChild(child)
	}
}

// clone returns a new node with the same type, data and attributes.
// The clone has no parent, no siblings and no children.
func (n *Node) clone() *Node {
	m := &Node{
		Type: n.Type,
		Data: n.Data,
		Attr: make([]Attribute, len(n.Attr)),
	}
	copy(m.Attr, n.Attr)
	return m
}

// ErrBufferExceeded means that the buffering limit was exceeded.
var ErrBufferExceeded = errors.New("max buffer exceeded")

const startHTMLTag = '<'
const endHTMLTag = '>'

var voidElements = []string{
	"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr",
}
var noSectionElements = []string{
	"code", "b", "i", "hr", "em", "strong", "small", "s",
}
var headingElements = []string{"h1", "h2", "h3", "h4", "h5", "h6"}

// An Attribute is an attribute namespace-key-value triple. Namespace is
// non-empty for foreign attributes like xlink, Key is alphabetic (and hence
// does not contain escapable characters like '&', '<' or '>'), and Val is
// unescaped (it looks like "a<b" rather than "a&lt;b").
//
// Namespace is only used by the parser, not the tokenizer.
type Attribute struct {
	Namespace, Key, Val string
}

// convertNewlines converts "\r" and "\r\n" in s to "\n".
// The conversion happens in place, but the resulting slice may be shorter.
func convertNewlines(s []byte) []byte {
	for i, c := range s {
		if c != '\r' {
			continue
		}

		src := i + 1
		if src >= len(s) || s[src] != '\n' {
			s[i] = '\n'
			continue
		}

		dst := i
		for src < len(s) {
			if s[src] == '\r' {
				if src+1 < len(s) && s[src+1] == '\n' {
					src++
				}
				s[dst] = '\n'
			} else {
				s[dst] = s[src]
			}
			src++
			dst++
		}
		return s[:dst]
	}
	return s
}

var (
	nul         = []byte("\x00")
	replacement = []byte("\ufffd")
)

// This regex detects the <x-ref REFERENCE> tags that need special processing
var reXRef = regexp.MustCompile(`<x-ref +([0-9a-zA-Z-_\.]+) *>`)
var reCodeBackticks = regexp.MustCompile(`\x60([0-9a-zA-Z-_\.]+)\x60`)

// parseParagraph returns a structure with the tag fields of the tag at the beginning of the line.
// It returns nil and an error if the line does not start with a tag.
func (n *Node) parseParagraph(para *Text) error {
	var tagSpec []byte

	// Add the paragraph to the node's paragraph
	n.Para = para
	// TODO: this is redundant, will eliminate it later
	n.Indentation = para.Indentation
	n.LineNumber = para.LineNumber

	rawLine := n.Para.Content

	// A token needs at least 3 chars
	if len(rawLine) < 3 || rawLine[0] != startHTMLTag {
		n.Type = ParagraphNode
		return nil
	}

	lineNumber := n.Para.LineNumber

	// Extract the whole tag string between the start and end tags
	// The end bracket is optional if there is no more text in the line after the tag attributes
	indexRightBracket := bytes.IndexByte(rawLine, endHTMLTag)
	if indexRightBracket == -1 {
		tagSpec = rawLine[1:]
	} else {

		// Extract the whole tag spec
		tagSpec = rawLine[1:indexRightBracket]

		// And the remaining text in the line
		n.RestLine = rawLine[indexRightBracket+1:]

	}

	name, tagSpec := readTagName(tagSpec)
	sname := string(name)
	n.Data = sname

	if sname == "section" {
		n.Type = SectionNode
	} else if sname == "div" {
		n.Type = DivNode
	} else if sname == "diagram" {
		n.Type = DiagramNode
	} else if sname == "x-code" {
		n.Type = CodeNode
	} else if sname == "pre" {
		n.Type = PreNode
	} else if sname == "ol" || sname == "ul" {
		n.Type = ListNode
	} else {
		n.Type = ParagraphNode
	}

	// offset := 0

	for {

		// We have finished the loop if there is no more data
		if len(tagSpec) == 0 {
			break
		}

		var attrVal []byte

		switch tagSpec[0] {
		case '#':
			if len(tagSpec) < 2 {
				return fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", lineNumber)
			}
			// Shortcut for id="xxxx"
			// Only the first id attribute is used, others are ignored
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Id) == 0 {
				n.Id = attrVal
			}

		case '.':
			if len(tagSpec) < 2 {
				return fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", lineNumber)
			}
			// Shortcut for class="xxxx"
			// The tag may specify more than one class and all are accumulated
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Class) > 0 {
				n.Class = append(n.Class, ' ')
			}
			n.Class = append(n.Class, attrVal...)
		case '@':
			if len(tagSpec) < 2 {
				return fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", lineNumber)
			}
			// Shortcut for src="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Src) == 0 {
				n.Src = attrVal
			}

		case '-':
			if len(tagSpec) < 2 {
				return fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", lineNumber)
			}
			// Shortcut for href="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Href) == 0 {
				n.Href = attrVal
			}
		case ':':
			if len(tagSpec) < 2 {
				return fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", lineNumber)
			}
			// Special attribute "type" for item classification and counters
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Bucket) == 0 {
				n.Bucket = attrVal
			}
		case '=':
			if len(tagSpec) < 2 {
				return fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", lineNumber)
			}
			// Special attribute "number" for list items
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Number) == 0 {
				n.Number = attrVal
			}
		default:
			// This should be a standard attribute
			var attr Attribute
			attr, tagSpec = readTagAttrKey(tagSpec)
			if len(attr.Key) == 0 {
				tagSpec = nil
			} else {
				n.Attr = append(n.Attr, attr)
			}

		}

	}

	return nil
}
