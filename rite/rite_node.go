package rite

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	hlhtml "github.com/alecthomas/chroma/v2/formatters/html"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2themes/d2themescatalog"
	"oss.terrastruct.com/d2/lib/textmeasure"
)

type Node struct {
	Parent, FirstChild, LastChild, PrevSibling, NextSibling *Node

	Type        NodeType
	Level       int
	Outline     string
	p           *Parser
	RawText     *Text
	InnerText   []byte
	Indentation int
	FileName    string
	LineNumber  int
	Name        string
	Id          []byte
	Class       []byte
	Src         []byte
	Href        []byte
	Bucket      []byte
	Number      []byte
	BulletText  []byte
	Attr        []Attribute
	RestLine    []byte
}

// The indentation string
var aBigIndentationString = bytes.Repeat([]byte(" "), 200)

func indent(n int) []byte {
	if n < 0 {
		fmt.Println("indent less than zero")
	}

	return aBigIndentationString[:n]
}

// A NodeType is the type of a Node.
type NodeType uint32

const (
	ErrorNode NodeType = iota
	DocumentNode
	IncludeNode
	SectionNode
	BlockNode
	DiagramNode
	ExplanationNode
	VerbatimNode
)

func newNode(p *Parser, fileName string, text *Text) *Node {

	n := &Node{}

	// Set the basic fields
	n.p = p
	n.RawText = text
	n.Indentation = text.Indentation
	n.FileName = fileName
	n.LineNumber = text.LineNumber

	return n
}

func NewVerbatimExplanationNode(p *Parser, fileName string, text *Text) *Node {

	// We receive in text the unparsed explanation paragraph
	// We convert it into a list item with the proper markup
	rawText := parseExplanationItem(text)

	n := NewNormalNode(p, fileName, rawText)

	// Parse the possible inner block
	p.ParseBlock(n)

	return n
}

func parseExplanationItem(lineSt *Text) *Text {
	const bulletPrefix = "# -("
	var r ByteRenderer

	// We receive a list item in Markdown format and we convert to proper HTML

	lineNum := lineSt.LineNumber
	line := lineSt.Content

	// Preprocess Markdown list markers
	// They can start with plain dashes '-' but we support a special format '-(something)'.
	// The 'something' inside parenthesis will be highlighted in the list item

	if !bytes.HasPrefix(line, []byte(bulletPrefix)) {
		stdlog.Panicf("parseExplanationItem, line %d: invalid prefix for line\n", lineNum)
	}

	// Get the end ')'
	indexRightBracket := bytes.IndexByte(line, ')')
	if indexRightBracket == -1 {
		stdlog.Panicf("parseMdList, line %d: no closing ')' in list bullet\n", lineNum)
	}

	// Check that there is at least one character inside the '()'
	if indexRightBracket == len(bulletPrefix) {
		stdlog.Panicf("parseMdList, line %d: no content inside '()' in list bullet\n", lineNum)
	}

	// Extract the whole bullet text, replacing embedded blanks
	bulletText := line[len(bulletPrefix):indexRightBracket]
	// bulletTextEncoded := bytes.ReplaceAll(bulletText, []byte(" "), []byte("_"))

	// And the remaining text in the line
	restLine := line[indexRightBracket+1:]

	// Build the line
	// r.Render("<x-li id='", bulletTextEncoded, "'>", "<a href='#", bulletTextEncoded, "' class='selfref'>")
	// r.Render("<b>", bulletText, "</b></a>", restLine)
	r.Render("<li><b>", bulletText, "</b>", restLine)

	l := r.Bytes()
	lineSt.Content = l
	return lineSt

}

// NewNormalNode creates a node from the text line that is passed.
// The new node is set to the proper type and its attributes populated.
// If the line starts with a proper tag, it is processed and the node is updated accordingly.
func NewNormalNode(p *Parser, fileName string, text *Text) *Node {
	var tagSpec []byte

	n := newNode(p, p.fileName, text)

	// Process the tag at the beginning of the line, if there is one

	// If the tag is less than 3 chars or the node does not start with '<', mark it as a paragraph
	// and do not process it further.
	if len(text.Content) < 3 || text.Content[0] != StartHTMLTag {
		n.Type = BlockNode
		n.Name = "p"
		n.RestLine = text.Content
		return n
	}

	// Now we know the line starts with a tag '<'

	// Extract the whole tag string between the start and end tags
	// The end bracket is optional if there is no more text in the line after the tag attributes
	indexRightBracket := bytes.IndexByte(text.Content, EndHTMLTag)
	if indexRightBracket == -1 {
		tagSpec = text.Content[1:]
	} else {

		// Extract the whole tag spec
		tagSpec = text.Content[1:indexRightBracket]

		// And the remaining text in the line
		n.RestLine = text.Content[indexRightBracket+1:]

	}

	// Extract the name of the tag from the tagSpec
	name, tagSpec := ReadTagName(tagSpec)

	// Set the name of the node with the tag name
	n.Name = string(name)

	// Do not process the tag if it is not a section element or it is a void one
	if contains(NoBlockElements, name) || contains(VoidElements, name) {
		n.Type = BlockNode
		n.Name = "p"
		n.RestLine = text.Content
		return n
	}

	// Determine type of node to create
	switch n.Name {
	case "section":
		n.Type = SectionNode
	case "x-diagram":
		n.Type = DiagramNode
	case "x-code", "pre":
		n.Type = VerbatimNode
	case "x-include":
		n.Type = IncludeNode
	default:
		n.Type = BlockNode
	}

	// Process all the attributes in the tag
	for {

		// We have finished the loop if there is no more data
		if len(tagSpec) == 0 {
			break
		}

		var attrVal []byte

		switch tagSpec[0] {
		case '#':
			if len(tagSpec) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Shortcut for id="xxxx"
			if tagSpec[1] != '"' && tagSpec[1] != '\'' {
				attrVal, tagSpec = ReadWord(tagSpec[1:])
			} else {
				attrVal, tagSpec = ReadQuotedWords(tagSpec[1:])
				// attrVal = encodeOnPlaceWithUnderscore(bytes.Clone(attrVal))
			}

			// Only the first id attribute is used, others are ignored
			if len(n.Id) == 0 {
				n.Id = attrVal
			}

		case '.':
			if len(tagSpec) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Shortcut for class="xxxx"
			// The tag may specify more than one class and all are accumulated
			attrVal, tagSpec = ReadWord(tagSpec[1:])
			if len(n.Class) > 0 {
				n.Class = append(n.Class, ' ')
			}
			n.Class = append(n.Class, attrVal...)
		case '@':
			if len(tagSpec) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Shortcut for src="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = ReadWord(tagSpec[1:])
			if len(n.Src) == 0 {
				n.Src = attrVal
			}

		case '-':
			if len(tagSpec) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Shortcut for href="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = ReadWord(tagSpec[1:])
			if len(n.Href) == 0 {
				n.Href = attrVal
			}
		case ':':
			if len(tagSpec) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Special attribute "type" for item classification and counters
			// Only the first attribute is used
			attrVal, tagSpec = ReadWord(tagSpec[1:])
			if len(n.Bucket) == 0 {
				n.Bucket = attrVal
			}
		case '=':
			if len(tagSpec) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Special attribute "number" for list items
			// Only the first attribute is used
			attrVal, tagSpec = ReadWord(tagSpec[1:])
			if len(n.Number) == 0 {
				n.Number = attrVal
			}
		default:
			// This should be a standard HTML attribute
			var attr Attribute
			attr, tagSpec = ReadTagAttrKey(tagSpec)
			if len(attr.Key) == 0 {
				tagSpec = nil
			} else {

				// Treat the most important attributes specially
				switch attr.Key {
				case "id":
					// Set the special Id field if it is not already set
					if len(n.Id) == 0 {
						// n.Id = encodeOnPlaceWithUnderscore(bytes.Clone(attr.Val))
						n.Id = bytes.Clone(attr.Val)
					}
				case "class":
					// More than one class can be specified and and all are accumulated, separated by a spece
					if len(n.Class) > 0 {
						n.Class = append(n.Class, ' ')
					}
					n.Class = append(n.Class, attr.Val...)
				case "src":
					// Only the first attribute is used
					if len(n.Src) == 0 {
						n.Src = attr.Val
					}
				case "href":
					// Only the first attribute is used
					if len(n.Href) == 0 {
						n.Href = attr.Val
					}
				default:
					n.Attr = append(n.Attr, attr)
				}
			}

		}

	}

	// For special types of nodes we generate automatically the id if the user did not specify it
	if len(n.Id) == 0 {
		if n.Name == "dt" || n.Name == "section" {
			// n.Id = encodeOnPlaceWithUnderscore(bytes.Clone(n.RestLine))
			n.Id = bytes.Clone(n.RestLine)
			// If the id already exists, make it unique
			if p.Xref[string(n.Id)] != nil {
				n.Id = strconv.AppendInt(n.Id, int64(n.LineNumber), 10)
			}

		}
	}

	// Update the table for cross-references using Ids in the tag.
	// If this tag has an 'id'
	if len(n.Id) > 0 {

		// We enforce uniqueness of ids
		if p.Xref[string(n.Id)] != nil {
			if n.Name == "x-li" {
				n.Id = append(n.Id, '_')
				n.Id = strconv.AppendInt(n.Id, int64(n.LineNumber), 10)
			} else {
				stdlog.Panicf("id already used, processing line %d\n", n.LineNumber)
			}
		}
		// Include the 'id' in the table and also the text for references
		p.Xref[string(n.Id)] = n
	}

	return n
}

// String returns a string representation of the TokenType.
func (n NodeType) String() string {
	switch n {
	case ErrorNode:
		return "Error"
	case DocumentNode:
		return "Document"
	case SectionNode:
		return "Section"
	case BlockNode:
		return "Block"
	case DiagramNode:
		return "Diagram"
	case VerbatimNode:
		return "Verbatim"
	case ExplanationNode:
		return "Explanations"
	}
	return "Invalid(" + strconv.Itoa(int(n)) + ")"
}

// tagString returns a string representation of a tag Token's Data and Attr.
func (n Node) tagString() string {
	buf := bytes.NewBufferString(n.Name)
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
		buf.Write(a.Val)
		buf.WriteByte('"')
	}
	return buf.String()
}

// String returns a string representation of the Node.
func (n Node) String() string {
	switch n.Type {
	case ErrorNode:
		return ""
	case DocumentNode:
		return "TopLevelDocument"
	case BlockNode, VerbatimNode, DiagramNode, ExplanationNode:
		return "<" + n.tagString() + ">"
	}
	return "Invalid(" + strconv.Itoa(int(n.Type)) + ")"
}

func (n *Node) AddClass(newClass []byte) {

	// More than one class can be specified and all are accumulated, separated by a spece
	if len(n.Class) > 0 {
		n.Class = append(n.Class, ' ')
	}
	n.Class = append(n.Class, newClass...)

}

func (n *Node) AddClassString(newClass string) {

	// More than one class can be specified and all are accumulated, separated by a spece
	if len(n.Class) > 0 {
		n.Class = append(n.Class, ' ')
	}
	n.Class = append(n.Class, newClass...)

}

// RenderHTML renders recursively to HTML this node and its children (if any)
func (n *Node) RenderHTML(br *ByteRenderer) error {

	//	indentStr := indent(n.Indentation)

	switch n.Type {
	case DiagramNode:
		if err := n.RenderDiagramNode(br); err != nil {
			return err
		}

	case VerbatimNode:
		if err := n.RenderCodeNode(br); err != nil {
			return err
		}
	case DocumentNode, IncludeNode:
		if err := n.RenderDocumentNode(br); err != nil {
			return err
		}

	default:
		if err := n.RenderNormalNode(br); err != nil {
			return err
		}

	}

	return nil
}

func (n *Node) RenderDocumentNode(br *ByteRenderer) error {

	// Travel the parse tree rendering each node
	for theNode := n.FirstChild; theNode != nil; theNode = theNode.NextSibling {
		theNode.RenderHTML(br)
	}

	return nil

}

// var reXRef = regexp.MustCompile(`<x-ref +([0-9a-zA-Z-_\.]+) *>`)
var reXRef = regexp.MustCompile(`<x-ref +"(.+?)" *>`)

func (n *Node) RenderNormalNode(br *ByteRenderer) error {

	// A slice with as many blanks as indented
	indentStr := indent(n.Indentation)

	// Get the rendered components of the tag
	_, startTag, endTag, rest := n.preRenderTheTag()

	if allsubmatchs := reXRef.FindAllSubmatch(rest, -1); len(allsubmatchs) > 0 {

		for _, submatchs := range allsubmatchs {

			// Convert blanks to underscores blanks
			// sub1 := string(encodeOnPlaceWithUnderscore(bytes.Clone(submatchs[1])))
			sub1 := string(bytes.Clone(submatchs[1]))

			// If the referenced node has a description, we will use it for the text of the link.
			// Otherwise we will use the plain ID of the referenced node
			referencedNode := n.p.Xref[sub1]
			if referencedNode == nil {
				fmt.Println("invalid x-ref at line", n.LineNumber)
				continue
			}

			var description string
			if referencedNode.Name == "x-li" {
				description = string(referencedNode.Id)
			} else {
				description = string(referencedNode.RestLine)
			}

			var replacement []byte
			if len(description) > 0 {
				replacement = []byte("<a href=\"#" + sub1 + "\" class=\"xref\">" + string(description) + "</a>")
			} else {
				replacement = []byte("<a href=\"#" + sub1 + "\" class=\"xref\">[${1}]</a>")

			}
			original := submatchs[0]
			rest = bytes.ReplaceAll(rest, original, replacement)
		}
	}

	// Render the start tag of this node
	br.Renderln(indentStr, startTag, rest)

	// Special case when the child nodes are list items and the parent node is not a list tag (ol, ul)
	// If the parent node is a <p>, we auto-generate an <ul> tag

	if n.Name == "p" && (n.FirstChild != nil) && (n.FirstChild.Name == "li" || n.FirstChild.Name == "x-li") {
		// Close the <p> tag
		br.Renderln(indentStr, endTag)

		// Open the <ul> tag
		br.Renderln(indent(n.FirstChild.Indentation), "<ul>")

	}

	// We visit depth-first the children of the node
	for childNode := n.FirstChild; childNode != nil; childNode = childNode.NextSibling {
		indentChild := indent(childNode.Indentation)

		// Wrap each children of a <li> items in a <div>
		if n.Name == "li" {
			br.Renderln(indentChild, "<div>")
		}

		if err := childNode.RenderHTML(br); err != nil {
			return err
		}

		if n.Name == "li" {
			br.Renderln(indentChild, "</div>")
		}
	}

	if n.Name == "p" && (n.FirstChild != nil) && (n.FirstChild.Name == "li" || n.FirstChild.Name == "x-li") {
		// Close the <ul> tag. No need to close the parent tag, as we already did it
		br.Renderln(indent(n.FirstChild.Indentation), "</ul>")

	} else {
		// Render the end tag of the node
		br.Renderln(strings.Repeat(" ", n.Indentation), endTag)
	}

	return nil

}

func (n *Node) addAttributes(startTag []byte) []byte {

	if len(n.Id) > 0 {
		startTag = fmt.Appendf(startTag, " id='%s'", n.Id)
	}
	if len(n.Class) > 0 {
		startTag = fmt.Appendf(startTag, " class='%s'", n.Class)
	}
	if len(n.Src) > 0 {
		startTag = fmt.Appendf(startTag, " src='%s'", n.Src)
	}
	if len(n.Href) > 0 {
		startTag = fmt.Appendf(startTag, " href='%s'", n.Href)
	}

	for _, a := range n.Attr {
		startTag = fmt.Appendf(startTag, " %s='%s'", a.Key, a.Val)
	}

	return startTag

}

func (n *Node) preRenderTheTag() (tagName string, startTag []byte, endTag []byte, rest []byte) {

	switch n.Name {

	case "pre":
		// Handle the 'pre' tag, with special case when the section started with '<pre><code>
		startTag = fmt.Appendf(startTag, "<pre")
		if bytes.HasPrefix(n.RestLine, []byte("<code")) {
			endTag = fmt.Appendf(endTag, "</code>")
		}
		endTag = fmt.Appendf(endTag, "</pre>")

	case "x-li":
		startTag = fmt.Appendf(startTag, "<li")
		startTag = n.addAttributes(startTag)
		startTag = fmt.Appendf(startTag, ">")

		endTag = fmt.Appendf(endTag, "</li>")
		if len(n.Number) > 0 {
			rest = fmt.Appendf(rest, "<b>%s</b>", n.Number)
		}
		rest = fmt.Appendf(rest, "%s", n.RestLine)

		return n.Name, startTag, endTag, rest

	case "x-dl":
		n.AddClassString("deftable")
		startTag = fmt.Appendf(startTag, "<table")
		startTag = n.addAttributes(startTag)
		startTag = fmt.Appendf(startTag, ">")

		endTag = fmt.Appendf(endTag, "</table>")

		return n.Name, startTag, endTag, nil

	case "x-dt":
		startTag = fmt.Appendf(startTag,
			"<tr><td style='padding-left: 0px;'><b>%s</b></td></tr><tr><td style='padding-left: 20px;'>",
			bytes.TrimSpace(n.RestLine))

		endTag = fmt.Appendf(endTag, "</td></tr>")

		return n.Name, startTag, endTag, nil

	case "x-code":
		startTag = fmt.Appendf(startTag, "<pre")
		endTag = fmt.Appendf(endTag, "</code></pre>")

	case "x-note":
		startTag = fmt.Appendf(startTag, "<table style='width:%s;'><tr><td class='xnotet'><aside class='xnotea'", "100%")
		endTag = fmt.Appendf(endTag, "</aside></td></tr></table>")

	case "x-warning":
		// Handle the 'x-note' special tag
		startTag = fmt.Appendf(startTag, "<table style='width:%s;'><tr><td class='xwarnt'><aside class='xwarna'", "100%")
		endTag = fmt.Appendf(endTag, "</aside></td></tr></table>")

	case "x-img":
		// Handle the 'x-img' special tag
		startTag = fmt.Appendf(startTag, "<figure><img")
		endTag = fmt.Appendf(endTag, "<figcaption>%s</figcaption></figure>", n.RestLine)

	default:
		startTag = fmt.Appendf(startTag, "<%s", n.Name)
		endTag = fmt.Appendf(endTag, "</%s>", n.Name)

	}

	if len(n.Id) > 0 {
		startTag = fmt.Appendf(startTag, " id='%s'", n.Id)
	}
	if len(n.Class) > 0 {
		startTag = fmt.Appendf(startTag, " class='%s'", n.Class)
	}
	if len(n.Src) > 0 {
		startTag = fmt.Appendf(startTag, " src='%s'", n.Src)
	}
	if len(n.Href) > 0 {
		startTag = fmt.Appendf(startTag, " href='%s'", n.Href)
	}

	for _, a := range n.Attr {
		startTag = fmt.Appendf(startTag, " %s='%s'", a.Key, a.Val)
	}

	restLine := bytes.Clone(n.RestLine)

	// Handle the special cases
	switch string(n.Name) {
	case "section":
		startTag = fmt.Appendf(startTag, ">")
		if len(n.RestLine) > 0 {
			if n.p.Config.Bool("rite.noReSpec") {
				startTag = fmt.Appendf(startTag, "<h2>%s %s</h2>\n", n.Outline, n.RestLine)
			} else {
				startTag = fmt.Appendf(startTag, "<h2>%s</h2>\n", n.RestLine)
			}
		}
		restLine = nil

	case "x-note":
		if len(n.RestLine) > 0 {
			startTag = fmt.Appendf(startTag, "><p class='xnotep'>NOTE: %s</p>", bytes.TrimSpace(n.RestLine))
		} else {
			startTag = fmt.Appendf(startTag, ">\n")
		}
		restLine = nil
	case "x-warning":
		if len(n.RestLine) > 0 {
			startTag = fmt.Appendf(startTag, "><p class='xnotep'>WARNING! %s</p>", bytes.TrimSpace(n.RestLine))
		} else {
			startTag = fmt.Appendf(startTag, ">\n")
		}
		restLine = nil

	case "x-code":
		startTag = fmt.Appendf(startTag, "><code>")
		restLine = nil

	case "x-img":
		startTag = fmt.Appendf(startTag, "/>")
		restLine = nil

	default:
		startTag = fmt.Appendf(startTag, ">")

	}

	return n.Name, startTag, endTag, restLine

}

// type preWrapper struct {
// 	s *chroma.Style
// }

// func (p preWrapper) Start(code bool, styleAttr string) string {
// 	// <pre tabindex="0" style="background-color:#fff;">
// 	if code {
// 		//		return fmt.Sprintf(`<pre class="nohighlight"%s><div style="padding:0.5em;"><code>`, styleAttr)
// 		return fmt.Sprintf(`<pre class="nohighlight"%s>`, styleAttr)
// 	}
// 	return fmt.Sprintf(`<pre class="nohighlight"%s>`, styleAttr)
// }

// func (p preWrapper) End(code bool) string {
// 	if code {
// 		return `</pre>`
// 	}
// 	return `</pre>`
// }

func (n *Node) RenderCodeNode(br *ByteRenderer) error {

	contentLines := string(n.InnerText)

	if len(contentLines) > 0 {

		// Determine lexer.
		l := lexers.Get(string(bytes.TrimSpace(n.Class)))
		if l == nil {
			l = lexers.Analyse(contentLines)
		}
		if l == nil {
			l = lexers.Fallback
		}
		l = chroma.Coalesce(l)

		// Determine style from the config data, with "dracula" as default
		styleName := n.p.Config.String("rite.codeStyle", "github")
		s := styles.Get(styleName)

		// fore := s.Get(chroma.Text).Colour.String()
		// bckg := s.Get(chroma.Background).Background.String()

		// pr := preWrapper{s}

		// Get the HTML formatter
		// f := hlhtml.New(hlhtml.Standalone(false), hlhtml.WithPreWrapper(pr))
		f := hlhtml.New(hlhtml.Standalone(false), hlhtml.PreventSurroundingPre(true))

		it, err := l.Tokenise(nil, contentLines)
		if err != nil {
			log.Fatal(err)
		}

		br.Renderln()
		// br.Renderln(`<table style="width:100%;"><tr><td style=color:`, fore, `;background-color:`, bckg, `;">`)
		br.Renderln(`<table style="width:100%;"><tr><td class="codecolor">`)
		br.Renderln("<pre class='nohighlight precolor'>")
		rb := &bytes.Buffer{}
		err = f.Format(rb, s, it)
		if err != nil {
			log.Fatal(err)
		}
		br.Render(rb.Bytes())
		br.Render("</pre>")
		br.Renderln(`</td></tr></table>`)
		br.Renderln()

	}

	return nil

}

func (n *Node) RenderDiagramNode(br *ByteRenderer) error {

	// Check if the class of diagram has been set
	if len(n.Class) == 0 {
		log.Fatalf("diagram type not found in line %d\n", n.LineNumber)
	}

	// Get the type of diagram
	diagType := strings.ToLower(string(n.Class))

	imageType := "png"
	if diagType == "d2" {
		imageType = "svg"
	}

	// We are going to write a file with the generated image contents (png/svg).
	// To enable caching, we calculate the hash of the diagram input data
	hh := md5.Sum(n.InnerText)
	hhString := fmt.Sprintf("%x", hh)

	// The file will be in the 'builtassets' directory
	fileName := "builtassets/" + diagType + "_" + string(hhString) + "." + imageType

	skinParams := []byte(`
skinparam shadowing true
skinparam ParticipantBorderColor black
skinparam arrowcolor black
skinparam SequenceLifeLineBorderColor black
skinparam SequenceLifeLineBackgroundColor PapayaWhip
`)

	var body []byte

	// Check if the file already exists. Because the hash of the diagram is in the file name, a modification
	// in the source diagram will cause a new file to be generated.
	// Eventually, spurious files should be deleted.
	if _, err := os.Stat(fileName); err != nil {
		// File does not exist, generate the image
		fmt.Println("Generating", fileName)

		if diagType == "d2" {
			// Special processing for D2 diagrams, which are generated by the embedded D2 processor
			// log.Println("Calling the D2 embedded generator:", fileName)

			// Create the SVG from the D2 description
			ruler, err := textmeasure.NewRuler()
			if err != nil {
				log.Fatalf("processD2 in line %d\n", n.LineNumber)
			}

			defaultLayout := func(ctx context.Context, g *d2graph.Graph) error {
				return d2dagrelayout.Layout(ctx, g, nil)
			}
			diagram, _, err := d2lib.Compile(context.Background(), string(n.InnerText), &d2lib.CompileOptions{
				Layout: defaultLayout,
				Ruler:  ruler,
			})
			if err != nil {
				log.Fatalf("processD2 in line %d, error %v\n", n.LineNumber, err)
			}
			body, err = d2svg.Render(diagram, &d2svg.RenderOpts{
				Pad:     d2svg.DEFAULT_PADDING,
				ThemeID: d2themescatalog.NeutralDefault.ID,
			})
			if err != nil {
				log.Fatalf("processD2 in line %d\n", n.LineNumber)
			}

		} else if diagType == "plantuml" {

			input := bytes.NewBuffer(skinParams)
			input.Write(n.InnerText)
			entrada := input.Bytes()

			// Get the user home directory
			homeDir, err := os.UserHomeDir()
			if err != nil {
				log.Fatalf("error calling UserHomeDir, line: %d, error: %v\n", n.LineNumber, err)
			}

			plantumlPath := filepath.Join(homeDir, ".plantuml", "plantuml.jar")

			cmd := exec.Command("java", "-jar", plantumlPath, "-pipe")

			cmd.Stdin = bytes.NewReader(entrada)
			var out bytes.Buffer
			var cmderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &cmderr
			err = cmd.Run()
			if err != nil {
				fmt.Printf("error calling Plantuml, line: %d, error: %v\n", n.LineNumber, err)
				fmt.Println(cmderr.String())
				fmt.Println(string(entrada))
				panic(err)
			}
			body = out.Bytes()

		} else if diagType == "plantuml_server" {

			// fmt.Println("Calling the PlantUML server:", fileName)

			// Encode the diagram content
			diagEncoded := fmt.Sprintf("~h%x", n.InnerText)

			// Build the url
			plantumlServer := "http://www.plantuml.com/plantuml/png/" + diagEncoded

			resp, err := http.Get(plantumlServer)
			if err != nil {
				fmt.Printf("error received from PlantUML, line: %d, error: %v\n", n.LineNumber, err)
				panic(err)
			}

			// Read the whole body in the reply
			defer resp.Body.Close()
			body, err = io.ReadAll(resp.Body)
			if err != nil {
				fmt.Printf("error reading response body from PlantUML, line: %d, error: %v\n", n.LineNumber, err)
				panic(err)
			}

			// Check the HTTP Status code in the reply
			if resp.StatusCode != http.StatusOK {
				fmt.Printf("PlantUML server responded (line: %d) with status: %v: %s\n", n.LineNumber, resp.StatusCode, string(body))
				panic("Error from PlantUML server")
			}

		} else {

			// fmt.Printf("calling the Kroki server, line: %d, file name: %s\n", n.LineNumber, fileName)

			// Build the url
			krokiURL := "https://kroki.io/" + diagType + "/" + imageType

			// Create the request to Kroki server
			in := bytes.NewReader(n.InnerText)

			// Send the request
			resp, err := http.Post(krokiURL, "text/plain", in)
			if err != nil {
				fmt.Printf("error received from Kroki, line: %d, error: %v\n", n.LineNumber, err)
				panic(err)
			}

			// Read the whole body in the reply
			defer resp.Body.Close()
			body, err = io.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("Error reading response body from Kroki:", err)
				panic(err)
			}

			// Check the HTTP Status code in the reply
			if resp.StatusCode != http.StatusOK {
				fmt.Println("Kroki server responded:", resp.StatusCode, string(body))
				panic("Error from Kroki server")
			}

		}

		// Make sure the directory exists before attempting to write the file
		err = os.Mkdir("builtassets", 0750)
		if err != nil && !os.IsExist(err) {
			log.Fatal(err)
		}

		// Permissions for user:rw group:rw others:r
		err = os.WriteFile(fileName, body, 0664)
		if err != nil {
			panic(err)
		}

	}

	// // Write the diagram as an HTML comment to enhance readability
	// doc.Render("<!-- Original Kroki diagram definition\n", diagContent, " -->\n\n")

	sectionIndentStr := strings.Repeat(" ", n.Indentation)

	br.Render(sectionIndentStr, "<figure><img src='"+fileName+"' alt=''>\n")

	br.Render(sectionIndentStr, "<figcaption>", n.RestLine, "</figcaption></figure>\n\n")

	// Write the explanations if there were any
	if n.FirstChild != nil {
		// Write the
		br.Render("<!-- ****** EXPLANATIONS **** -->\n")
		br.Render("\n", bytes.Repeat([]byte(" "), n.Indentation), "<ul class='plain'>\n")
		// We visit depth-first the children of the
		for theNode := n.FirstChild; theNode != nil; theNode = theNode.NextSibling {
			theNode.RenderHTML(br)
		}
		br.Render(bytes.Repeat([]byte(" "), n.Indentation), "</ul>\n")
	}

	return nil
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

// ReparentChildren reparents all of src's child nodes to dst.
func ReparentChildren(dst, src *Node) {
	for {
		child := src.FirstChild
		if child == nil {
			break
		}
		src.RemoveChild(child)
		dst.AppendChild(child)
	}
}

// ErrBufferExceeded means that the buffering limit was exceeded.
var ErrBufferExceeded = errors.New("max buffer exceeded")

const StartHTMLTag = '<'
const EndHTMLTag = '>'

var VoidElements = []string{
	"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr",
}
var NoBlockElements = []string{
	"p", "code", "b", "i", "hr", "em", "strong", "small", "s",
}
var HeadingElements = []string{"h1", "h2", "h3", "h4", "h5", "h6"}

func contains(set []string, tagName []byte) bool {
	for _, el := range set {
		if string(tagName) == el {
			return true
		}
	}
	return false
}

// An Attribute is an attribute key-value pair. Key is alphabetic (and hence
// does not contain escapable characters like '&', '<' or '>'), and Val is
// unescaped (it looks like "a<b" rather than "a&lt;b").
type Attribute struct {
	Key string
	Val []byte
}
