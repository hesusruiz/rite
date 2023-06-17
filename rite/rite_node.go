// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rite

import (
	"strconv"
)

// A NodeType is the type of a Node.
type NodeType uint32

const (
	ErrorNode NodeType = iota
	DocumentNode
	D2Node
	DiagramNode
	CodeNode
	PreNode
	HeaderNode
	ListNode
	SectionNode
	ParagraphNode
)

// String returns a string representation of the TokenType.
func (n NodeType) String() string {
	switch n {
	case ErrorNode:
		return "Error"
	case DocumentNode:
		return "Document"
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

	Para        *Paragraph
	Token       *Token
	Indentation int
	Type        NodeType
	Data        string
	Namespace   string
	Attr        []Attribute
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
