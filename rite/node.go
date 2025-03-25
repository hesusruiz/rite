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

type TreeNode struct {
	Parent, FirstChild, LastChild, PrevSibling, NextSibling *Node
}

type Node struct {
	TreeNode
	Type        NodeType
	Level       int
	Outline     string
	p           *Parser
	RawText     *Text
	InnerText   []byte
	Indentation int
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
	return aBigIndentationString[:n]
}

// A NodeType is the type of a Node.
type NodeType uint32

const (
	ErrorNode NodeType = iota
	DocumentNode
	SectionNode
	BlockNode
	DiagramNode
	ExplanationNode
	VerbatimNode
	IncludeNode
)

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
	case IncludeNode:
		return "Include"
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
		return "ErrorNode"
	case DocumentNode:
		return "TopLevelDocument"
	case SectionNode, BlockNode, VerbatimNode, DiagramNode, ExplanationNode, IncludeNode:
		return "<" + n.tagString() + ">"
	}
	return "Invalid(" + strconv.Itoa(int(n.Type)) + ")"
}

func (n *Node) AddClass(newClass []byte) {

	// More than one class can be specified and all are accumulated, separated by a space
	if len(n.Class) > 0 {
		n.Class = append(n.Class, ' ')
	}
	n.Class = append(n.Class, newClass...)

}

func (n *Node) AddClassString(newClass string) {

	// More than one class can be specified and all are accumulated, separated by a space
	if len(n.Class) > 0 {
		n.Class = append(n.Class, ' ')
	}
	n.Class = append(n.Class, newClass...)

}

// RenderHTML renders recursively to HTML this node and its children (if any)
func (n *Node) RenderHTML(br *ByteRenderer) error {

	// The DocumentNode is the root node of the document, so we treat it as special
	if n.Type == DocumentNode {
		fmt.Printf("Document: %s %d\n", n.p.fileName, n.LineNumber)
		// We visit depth-first the children of the node
		for theNode := n.FirstChild; theNode != nil; theNode = theNode.NextSibling {
			if err := theNode.RenderHTML(br); err != nil {
				return err
			}
		}
		return nil
	}

	// Now we know that the node is a descendant of the DocumentNode

	switch n.Type {

	case DiagramNode:
		if err := n.RenderDiagramNode(br); err != nil {
			return err
		}

	case VerbatimNode:
		if err := n.RenderExampleNode(br); err != nil {
			return err
		}

	case ExplanationNode:
		// Prepare the indentation prefix (blanks) of the line for rendering
		indentStr := indent(n.Indentation)

		// Render the start tag of this node
		br.Renderln(indentStr, n.RawText.Content)
		br.Render("<div>\n")

		// We visit depth-first the children of the node
		for theNode := n.FirstChild; theNode != nil; theNode = theNode.NextSibling {
			if err := theNode.RenderHTML(br); err != nil {
				return err
			}
		}
		br.Render("</div>\n")

		// Render the end tag of the node
		br.Renderln(indentStr, "</li>")

	default:
		if err := n.RenderNormalNode(br); err != nil {
			return err
		}

	}

	return nil
}

// var reXRef = regexp.MustCompile(`<x-ref +([0-9a-zA-Z-_\.]+) *>`)
var reXRef = regexp.MustCompile(`<x-ref +"(.+?)" *>`)
var reBiblioRef = regexp.MustCompile(`\[\[(.+?)\]\]`)

func (n *Node) RenderNormalNode(br *ByteRenderer) error {

	// A slice with as many blanks as indented
	indentStr := indent(n.Indentation)

	// Get the rendered components of the tag
	_, startTag, endTag, rest := n.preRenderTheTag()

	// Handle cross-references in the line
	if allsubmatches := reXRef.FindAllSubmatch(rest, -1); len(allsubmatches) > 0 {

		for _, submatchs := range allsubmatches {

			// Convert blanks to underscores blanks
			// sub1 := string(encodeOnPlaceWithUnderscore(bytes.Clone(submatchs[1])))
			sub1 := string(bytes.Clone(submatchs[1]))

			// If the referenced node has a description, we will use it for the text of the link.
			// Otherwise we will use the plain ID of the referenced node
			referencedNode := n.p.Xref[sub1]
			if referencedNode == nil {
				stdlog.Printf("%s (line %d) error: nil xref for '%s'\n", n.p.fileName, n.LineNumber, sub1)
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

	// Handle cross-references in the line
	if allsubmatches := reBiblioRef.FindAllSubmatch(rest, -1); len(allsubmatches) > 0 {

		for _, submatchs := range allsubmatches {

			sub1 := string(bytes.Clone(submatchs[1]))

			// If the referenced node has a description, we will use it for the text of the link.
			// Otherwise we will use the plain ID of the referenced node
			if n.p.Bibdata == nil {
				stdlog.Printf("%s (line %d) error: nil Biblio reference for '%s'\n", n.p.fileName, n.LineNumber, sub1)
				continue
			}
			referencedNode := n.p.Bibdata.Map(sub1)
			if referencedNode == nil {
				stdlog.Printf("%s (line %d) error: nil Biblio reference for '%s'\n", n.p.fileName, n.LineNumber, sub1)
				continue
			}

			n.p.MyBibdata[sub1] = referencedNode

			replacement := []byte("<a href=\"#bib_" + sub1 + "\" class=\"xref\">[" + sub1 + "]</a>")
			original := submatchs[0]
			rest = bytes.ReplaceAll(rest, original, replacement)

		}
	}

	// Render the start tag of this node
	br.Renderln(indentStr, startTag, rest)

	// We visit depth-first the children of the node
	for theNode := n.FirstChild; theNode != nil; theNode = theNode.NextSibling {
		if theNode.Indentation < 0 {
			fmt.Println("indentation negative", n)
			fmt.Println("indentation negative", theNode)
		}

		if theNode == n.FirstChild {
			if n.Name != "ul" && n.Name != "ol" {
				if theNode.Name == "li" {
					br.Renderln("<ul>")
				}
			}
		}
		if n.Name == "li" {
			br.Renderln(indent(theNode.Indentation), "<div>")
		}

		if err := theNode.RenderHTML(br); err != nil {
			return err
		}

		if n.Name == "li" {
			br.Renderln(indent(theNode.Indentation), "</div>")
		}
		if theNode == n.LastChild {
			if n.Name != "ul" && n.Name != "ol" {
				if theNode.Name == "li" {
					br.Renderln("</ul>")
				}
			}
		}

	}

	// Render the end tag of the node
	br.Renderln(strings.Repeat(" ", n.Indentation), endTag)

	return nil

}

type AttrType int

const (
	Id AttrType = iota
	Class
	Src
	Href
	Attrs
)

func (n *Node) addAttributes2(st *ByteRenderer, attrs ...AttrType) {

	for _, attr := range attrs {
		if attr == Id && len(n.Id) > 0 {
			st.Render(" id='", n.Id, "'")
		}
		if attr == Class && len(n.Class) > 0 {
			st.Render(" class='", n.Class, "'")
		}
		if attr == Src && len(n.Src) > 0 {
			// If the path starts with a '.', replace it with the full path from the root of the project
			if bytes.HasPrefix(n.Src, []byte("./")) {
				// TODO: replace path with full relative path
				fmt.Println("TODO replace image path")
			}
			st.Render(" src='", n.Src, "'")
		}
		if attr == Href && len(n.Href) > 0 {
			st.Render(" href='", n.Href, "'")
		}
		if attr == Attrs {
			for _, a := range n.Attr {
				st.Render(" ", a.Key, "='", a.Val, "'")
			}
		}
	}

}

func (n *Node) preRenderTheTag() (tagName string, startTag []byte, endTag []byte, rest []byte) {
	st := &ByteRenderer{}
	et := &ByteRenderer{}

	switch n.Name {

	case "section":
		st.Render("<", n.Name)
		n.addAttributes2(st, Id, Class, Src, Href, Attrs)
		st.Render(">")

		if len(n.RestLine) > 0 {
			if n.p.Config.Bool("rite.noReSpec") || n.p.Config.Bool("rite.norespec") {
				st.Render("<h2>", n.Outline, " ", n.RestLine, "</h2>\n")
			} else {
				st.Render("<h2>", n.RestLine, "</h2>\n")
			}
		}

		et.Render("</", n.Name, ">")

	case "pre":
		// Handle the 'pre' tag, with special case when the section started with '<pre><code>
		st.Render("<pre")
		n.addAttributes2(st, Id, Class, Src, Href, Attrs)
		st.Render(">")

		if bytes.HasPrefix(n.RestLine, []byte("<code")) {
			et.Render("</code>")
		}
		et.Render("</pre>")

		rest = bytes.Clone(n.RestLine)

	case "x-li":
		st.Render("<li")
		n.addAttributes2(st, Id, Class, Src, Href, Attrs)
		st.Render(">")

		et.Render("</li>")

		if len(n.Id) > 0 {
			rest = fmt.Appendf(rest, "<b>%s</b>", n.Id)
		}
		rest = fmt.Appendf(rest, "%s", n.RestLine)

	case "x-dl":
		// We represent definition lists as tables, for compatibility when copying from HTML
		// and pasting to Google Docs.
		// This is a class for table formatting
		n.AddClassString("deftable")

		st.Render("<table")
		n.addAttributes2(st, Id, Class, Src, Href, Attrs)
		st.Render(">")

		et.Render("</table>")

	case "x-dt":
		st.Render(
			"<tr><td style='padding-left: 0px;'><b>", bytes.TrimSpace(n.RestLine), "</b></td></tr><tr><td style='padding-left: 20px;'>",
		)

		et.Render("</td></tr>")

	case "x-code", "x-example":
		st.Render("<pre")
		n.addAttributes2(st, Id, Class, Src, Href, Attrs)
		st.Render("><code>")

		et.Render("</code></pre>")

	case "x-note":
		st.Render("<table style='width:100%;margin:1em 0;'><tr><td class='xnotet'><aside class='xnotea'>")
		if len(n.RestLine) > 0 {
			st.Render("<p class='xnotep'>NOTE: ", bytes.TrimSpace(n.RestLine), "</p>")
		}

		et.Render("</aside></td></tr></table>")

	case "x-warning":
		// Handle the 'x-note' special tag
		st.Render("<table style='width:100%;'><tr><td class='xwarnt'><aside class='xwarna'>")
		if len(n.RestLine) > 0 {
			st.Render("<p class='xnotep'>WARNING! ", bytes.TrimSpace(n.RestLine), "</p>")
		}

		et.Render("</aside></td></tr></table>")

	case "x-img":
		// Handle the 'x-img' special tag
		st.Render("<figure")
		// n.AddClassString("figureshadow")
		n.addAttributes2(st, Id, Class, Href, Attrs)
		st.Render("><img class='figureshadow'")
		n.addAttributes2(st, Src)
		st.Render(" alt='", n.RestLine, "'>")

		et.Render("<figcaption>", n.RestLine, "</figcaption></figure>\n")

	default:
		st.Render("<", n.Name)
		n.addAttributes2(st, Id, Class, Src, Href, Attrs)
		st.Render(">")

		rest = bytes.Clone(n.RestLine)

		et.Render("</", n.Name, ">")

	}

	return n.Name, st.CloneBytes(), et.CloneBytes(), rest

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

func (n *Node) RenderExampleNode(br *ByteRenderer) error {

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
		// styleName := config.String("rite.codeStyle", "github")
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

	// Generate a PNG images except for D2 which only accepts SVG
	imageType := "png"
	if diagType == "d2" {
		imageType = "svg"
	}

	// We are going to write a file with the generated image contents (png/svg).
	// To enable caching, we calculate the hash of the diagram input data
	hh := md5.Sum(n.InnerText)
	hhString := fmt.Sprintf("%x", hh)

	// The file will be in the 'builtassets' directory
	fileName := "builtassets/" + diagType + "_" + hhString + "." + imageType

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
	// Eventually, spurious files should be deleted manually.
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
			fmt.Printf("generating Plantuml line %d\n", n.LineNumber)

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

	br.Render(sectionIndentStr, "<figure><img class='figureshadow' src='"+fileName+"' alt='", n.RestLine, "'>\n")

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

// Clone returns a new node with the same type, data and attributes.
// The Clone has no parent, no siblings and no children.
func (n *Node) Clone() *Node {
	m := &Node{
		Type: n.Type,
		Name: n.Name,
		Attr: make([]Attribute, len(n.Attr)),
	}
	copy(m.Attr, n.Attr)
	return m
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

// contains determines if tagName is contained in set
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
