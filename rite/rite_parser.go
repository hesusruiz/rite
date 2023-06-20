package rite

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/hesusruiz/vcutils/yaml"
	"go.uber.org/zap"
)

var config *yaml.YAML

const blank byte = ' '

type ByteRenderer struct {
	bytes.Buffer
}

func (r *ByteRenderer) Render(inputs ...any) {
	for _, s := range inputs {
		switch v := s.(type) {
		case string:
			r.WriteString(v)
		case []byte:
			r.Write(v)
		case int:
			r.WriteString(strconv.FormatInt(int64(v), 10))
		case byte:
			r.WriteByte(v)
		case rune:
			r.WriteRune(v)
		default:
			log.Panicf("attemping to write something not a string, int, rune, []byte or byte: %T", s)
		}
	}
}

func (r *ByteRenderer) Renderln(inputs ...any) {
	r.Render(inputs...)
	r.Render('\n')
}

type Text struct {
	Indentation int
	LineNumber  int
	Content     []byte
}

func (para *Text) String() string {
	if para == nil {
		return "<nil><nil><nil>"
	}

	numChars := 10
	if len(para.Content) < numChars {
		numChars = len(para.Content)
	}

	return strings.Repeat(" ", para.Indentation) + string(para.Content[:numChars])
}

type Parser struct {
	// The source of the document for scanning
	s *bufio.Scanner

	// doc is the document root element.
	doc *Node

	bufferedPara *Text
	bufferedLine *Text

	// currentLine is the current raw currentLine
	currentLine []byte

	// lineCounter is the number of lines processed
	lineCounter int

	// currentIndentation is the current currentIndentation
	currentIndentation int

	// This is true when we have read the whole file
	atEOF bool

	lastError error

	Ids    map[string]int // To provide numbering of different entity classes
	figs   map[string]int // To provide numbering of figs of different types in the document
	Config *yaml.YAML

	log *zap.SugaredLogger
}

func (p *Parser) currentLineNum() int {
	return p.lineCounter
}

var errorEOF = fmt.Errorf("end of file")

// SkipBlankLines returns the line number of the first non-blank line,
// starting from the provided line number, or EOF if there are no more blank lines.
// If the start line is non-blank, we return that line.
func (p *Parser) SkipBlankLines() bool {

	for !p.atEOF {

		line := p.ReadLine()

		// If the line is not empty, we are done
		if line != nil {
			p.UnreadLine(line)
			return true
		}
	}

	// All lines of the file were processed without finding a blank line
	return false
}

func (p *Parser) ReadLine() *Text {

	if p.lastError != nil {
		return nil
	}

	if p.bufferedLine != nil && p.bufferedPara != nil {
		log.Fatalf("reading a line when both bufferd line and paragraph exist")
	}

	// If there is a line alredy buffered, return it
	if p.bufferedLine != nil {
		line := p.bufferedLine
		p.bufferedLine = nil
		return line
	}

	s := p.s

	if s.Scan() {

		// Get a rawLine from the file
		rawLine := bytes.Clone(s.Bytes())
		p.lineCounter++

		// Strip blanks at the beginning of the line and calculate indentation based on the difference in length
		// We do not support other whitespace like tabs
		// p.line = bytes.TrimLeft(rawLine, " ")
		p.currentLine = trimLeft(rawLine, blank)
		if len(p.currentLine) == 0 {
			return nil
		}

		p.currentIndentation = len(rawLine) - len(p.currentLine)

		line := &Text{}
		line.LineNumber = p.currentLineNum()
		line.Content = p.currentLine
		line.Indentation = p.currentIndentation
		return line

	}

	// Check if there were other errors apart from EOF
	if err := s.Err(); err != nil {
		p.lastError = err
		log.Fatalf("error scanning: %v", err)
	}

	// We have processed all lines of the file
	p.lastError = nil
	p.atEOF = true
	return nil
}

func (p *Parser) UnreadLine(line *Text) {
	if p.bufferedLine != nil {
		log.Fatalf("UnreadLine: too many calls in line: %d\n", p.currentLineNum())
	}
	p.bufferedLine = line
}

func (p *Parser) UnreadParagraph(para *Text) {
	if p.bufferedPara != nil {
		log.Fatalf("UnreadParagraph: too many calls in line: %d\n", p.currentLineNum())
	}
	p.bufferedPara = para
}

func (p *Parser) ReadParagraph(min_indentation int) *Text {

	if p.lastError != nil {
		return nil
	}

	// If there is a paragraph alredy buffered, return it
	if p.bufferedPara != nil {
		para := p.bufferedPara
		p.bufferedPara = nil
		return para
	}

	// Skip all blank lines until EOF or another error
	if !p.SkipBlankLines() {
		return nil
	}

	// Read all lines accumulating them until a blank line, EOF or another error
	var br ByteRenderer
	var para *Text

	for {

		line := p.ReadLine()

		if line == nil {
			// Sanity check
			if para == nil {
				log.Fatalf("no paragraph read, line: %d\n", p.currentLineNum())
			}

			break
		}

		// If the line read is not more indented than the min_indentation, we have finished the paragraph
		if line.Indentation <= min_indentation {
			p.UnreadLine(line)
			break
		}

		// Special case: A line starting with '-' if a line item and is considered a different paragraph
		if para != nil && line.Indentation == para.Indentation && line.Content[0] == '-' {
			p.UnreadLine(line)
			break
		}

		// Initialize the Paragraph if this is the first line read
		if para == nil {
			para = &Text{}
			para.LineNumber = p.currentLineNum()
			para.Indentation = line.Indentation
		}

		if line.Indentation != para.Indentation {
			p.UnreadLine(line)
			break
		}

		// Add the contents of the line to the paragraph
		br.Write(p.currentLine)

	}

	if para != nil {
		// Get the accumulated contents of all lines
		para.Content = br.Bytes()

		// Preprocess the paragraph
		para = p.PreprocesLine(para)
	}

	return para

}

func (p *Parser) PreprocesLine(lineSt *Text) *Text {
	line := lineSt.Content
	lineNum := lineSt.LineNumber

	// We ignore any line starting with an end tag
	if bytes.HasPrefix(line, []byte("</")) {
		return nil
	}

	// Preprocess the special <x-ref> tag inside the text of the line
	if bytes.Contains(line, []byte("<x-ref")) {
		line = reXRef.ReplaceAll(line, []byte("<a href=\"#${1}\" class=\"xref\">[${1}]</a>"))
	}
	if bytes.Contains(line, []byte("`")) {
		line = reCodeBackticks.ReplaceAll(line, []byte("<code>${1}</code>"))
	}

	// Preprocess Markdown headers ('#') and convert to h1, h2, ...
	// We assume that a header starts with the '#' character, no matter what the rest of the line is
	if line[0] == '#' {

		// Trim and count the number of '#'
		plainLine := trimLeft(line, '#')
		lenPrefix := len(line) - len(plainLine)
		hnum := byte('0' + lenPrefix)

		// Trim the possible whitespace between the '#'s and the text
		plainLine = trimLeft(plainLine, ' ')

		// Build the new line and store it
		line = append([]byte("<h"), hnum, '>')
		line = append(line, plainLine...)

	}

	// Preprocess Markdown list markers
	// They can start with plain dashes '-' but we support a special format '-(something)'.
	// The 'something' inside parenthesis will be highlighted in the list item
	if bytes.HasPrefix(line, []byte("- ")) {

		line = bytes.Replace(line, []byte("- "), []byte("<li>"), 1)

	} else if bytes.HasPrefix(line, []byte("-(")) {

		// Get the end ')'
		indexRightBracket := bytes.IndexByte(line, ')')
		if indexRightBracket == -1 {
			log.Fatalf("NewDocument, line %d: no closing ')' in list bullet", lineNum)
		}

		// Check that there is at least one character inside the '()'
		if indexRightBracket == 2 {
			log.Fatalf("NewDocument, line %d: no content inside '()' in list bullet", lineNum)
		}

		// Extract the whole tag spec, eliminating embedded blanks
		bulletText := line[2:indexRightBracket]
		bulletText = bytes.ReplaceAll(bulletText, []byte(" "), []byte("%20"))

		// And the remaining text in the line
		restLine := line[indexRightBracket+1:]

		// Build the line
		var br ByteRenderer
		br.Render("<li id='", lineSt.LineNumber, "'>")
		br.Render("<a href='#", lineSt.LineNumber, "' class='selfref'>")
		br.Render("<b>", bulletText, "</b></a> ", restLine)
		line = br.Bytes()

	}

	lineSt.Content = line
	return lineSt
}

func (p *Parser) NewNode(text *Text) *Node {
	var tagSpec []byte

	n := &Node{}

	// Set the basic fields
	n.Indentation = text.Indentation
	n.LineNumber = text.LineNumber
	n.RawText = text

	// Process the tag at the beginning of the line, if there is one

	// If the is less than 3 chars or the node does not start with '<', mark it as a paragraph
	// and do not process it anymore
	if len(text.Content) < 3 || text.Content[0] != startHTMLTag {
		n.Type = SectionNode
		n.Name = "p"
		n.RestLine = text.Content
		return n
	}

	// Extract the whole tag string between the start and end tags
	// The end bracket is optional if there is no more text in the line after the tag attributes
	indexRightBracket := bytes.IndexByte(text.Content, endHTMLTag)
	if indexRightBracket == -1 {
		tagSpec = text.Content[1:]
	} else {

		// Extract the whole tag spec
		tagSpec = text.Content[1:indexRightBracket]

		// And the remaining text in the line
		n.RestLine = text.Content[indexRightBracket+1:]

	}

	// Extract the pepe of the tag from the tagSpec
	name, tagSpec := readTagName(tagSpec)

	// Set the name of the node with the tag name
	n.Name = string(name)

	// Do not process the tag if it is not a section element or it i sa void one
	if contains(noSectionElements, name) || contains(voidElements, name) {
		n.Type = SectionNode
		n.Name = "p"
		n.RestLine = text.Content
		return n
	}

	// Determine type of node
	switch n.Name {
	case "x-diagram":
		n.Type = DiagramNode
	case "x-code", "pre":
		n.Type = VerbatimNode
	default:
		n.Type = SectionNode
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
				log.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Shortcut for id="xxxx"
			// Only the first id attribute is used, others are ignored
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Id) == 0 {
				n.Id = attrVal
			}

		case '.':
			if len(tagSpec) < 2 {
				log.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
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
				log.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Shortcut for src="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Src) == 0 {
				n.Src = attrVal
			}

		case '-':
			if len(tagSpec) < 2 {
				log.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Shortcut for href="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Href) == 0 {
				n.Href = attrVal
			}
		case ':':
			if len(tagSpec) < 2 {
				log.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}
			// Special attribute "type" for item classification and counters
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(n.Bucket) == 0 {
				n.Bucket = attrVal
			}
		case '=':
			if len(tagSpec) < 2 {
				log.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
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

	return n
}

func (p *Parser) ParseBlock(parent *Node) {

	// The first line will determine the indentation of the block
	blockIndentation := -1

	for {

		// Read a paragraph more indented than the parent, skipping all blank lines if needed
		para := p.ReadParagraph(parent.Indentation)

		// If no paragraph, we have reached the end of the block or the file
		if para == nil {
			return
		}

		// We ignore any line starting with a comment marker: '//'
		if bytes.HasPrefix(para.Content, []byte("//")) {
			continue
		}

		// Set the indentation of the first line of the inner block
		if blockIndentation == -1 {
			blockIndentation = para.Indentation
		}

		// This line belongs to this block
		if para.Indentation == blockIndentation {

			// Create a node for the paragraph
			sibling := p.NewNode(para)
			parent.AppendChild(sibling)

			// DEBUG
			// fmt.Printf("%d: %s%s\n", sibling.LineNumber, strings.Repeat(" ", blockIndentation), sibling)
			if sibling.Type == DiagramNode {
				p.ParseVerbatim(sibling)
			}
			if sibling.Type == VerbatimNode {
				p.ParseVerbatim(sibling)
			}

			// Go to next paragraph
			continue

		}

		// Parse an interior block
		if para.Indentation > blockIndentation {
			p.UnreadParagraph(para)

			// Get the last sibling processed
			// Sanity check
			if parent.LastChild == nil {
				log.Fatalf("more indented paragraph without sibling node, line: %d\n", p.currentLineNum())
			}

			// Parse the block using the child node
			p.ParseBlock(parent.LastChild)
			continue
		}

	}

}

func (p *Parser) parseVerbatimExplanation(node *Node) {
	const bulletPrefix = "# -("
	const simplePrefix = "# - "
	const additionalPrefix = "# -+"
	var r ByteRenderer

	lineNum := node.LineNumber
	line := node.RawText.Content

	// Sanity check
	if !bytes.HasPrefix(line, []byte("# -")) {
		log.Fatalf("parseVerbatimExplanation, line %d: invalid prefix\n", lineNum)
	}

	// Preprocess Markdown list markers
	// They can start with plain dashes '-' but we support a special format '-(something)'.
	// The 'something' inside parenthesis will be highlighted in the list item
	if bytes.HasPrefix(line, []byte(simplePrefix)) {

		restLine := line[len(simplePrefix):]

		// Build the line
		r.Render("<li id='", lineNum, "'>")
		r.Render("<a href='#", lineNum, "' class='selfref'>")
		r.Render("<b>-</b></a> ", restLine)

		l := r.Bytes()
		node.RawText.Content = l

		return

	} else if bytes.HasPrefix(line, []byte(additionalPrefix)) {

		restLine := line[len(simplePrefix):]

		// Build the line
		r.Render("<p>", restLine, "</p>")

		l := r.Bytes()
		node.RawText.Content = l

		return

	} else if bytes.HasPrefix(line, []byte(bulletPrefix)) {

		// Get the end ')'
		indexRightBracket := bytes.IndexByte(line, ')')
		if indexRightBracket == -1 {
			log.Fatalf("parseVerbatimExplanation, line %d: no closing ')' in list bullet\n", lineNum)
		}

		// Check that there is at least one character inside the '()'
		if indexRightBracket == len(bulletPrefix) {
			log.Fatalf("parseVerbatimExplanation, line %d: no content inside '()' in list bullet\n", lineNum)
		}

		// Extract the whole tag spec, eliminating embedded blanks
		bulletText := line[len(bulletPrefix):indexRightBracket]
		bulletTextEncoded := bytes.ReplaceAll(bulletText, []byte(" "), []byte("_"))

		// And the remaining text in the line
		restLine := line[indexRightBracket+1:]

		// Build the line
		r.Render("<li id='", lineNum, ".", bulletTextEncoded, "'>")
		r.Render("<a href='#", lineNum, ".", bulletTextEncoded, "' class='selfref'>")
		r.Render("<b>", bulletText, "</b></a>", restLine, '\n')

		// Parse the inner block
		p.ParseBlock(node)

		l := r.Bytes()
		node.RawText.Content = l

		return

	}

	log.Fatalf("parseVerbatimExplanation, line %v: invalid explanation in list bullet\n", lineNum)
}

func (p *Parser) ParseVerbatim(parent *Node) bool {

	// The first line will determine the indentation of the block
	sectionIndent := parent.Indentation
	blockIndentation := -1

	// This will hold the string with the text lines for diagram
	diagContentLines := []*Text{}

	// We are going to calculate the minimum indentation for the whole block.
	// The starting point is a very big value which will be reduced to the correct value during the loop
	minimumIndentation := 9999999

	// Loop until the end of the document or until we find a line with less or equal indentation
	// Blank lines are assumed to pertain to the verbatim section
	for {

		line := p.ReadLine()

		// If the line is blank, continue with the loop
		if line == nil {
			blankText := &Text{}
			diagContentLines = append(diagContentLines, blankText)
			continue
		}

		// Set the indentation of the first line of the inner block
		if blockIndentation == -1 {
			blockIndentation = line.Indentation
		}

		// The paragraph is finished if the line has less or equal indentation than the section
		if line.Indentation <= sectionIndent {
			p.UnreadLine(line)
			break
		}

		// Process normal lines
		if !bytes.HasPrefix(line.Content, []byte("# -")) {

			// Update minimum indentation if needed
			if line.Indentation < minimumIndentation {
				minimumIndentation = line.Indentation
			}

			// Append the line
			diagContentLines = append(diagContentLines, line)

		} else {

			// Create a node to parse the explanation text
			child := &Node{}
			parent.AppendChild(child)
			child.Type = ExplanationNode

			// Add the paragraph to the node's paragraph
			child.RawText = line
			// TODO: this is redundant, will eliminate it later
			child.Indentation = line.Indentation
			child.LineNumber = line.LineNumber

			p.parseVerbatimExplanation(child)
		}

		// Go to process next line
		continue

	}

	var br ByteRenderer
	for _, line := range diagContentLines {
		if len(line.Content) > 0 {
			br.Renderln(bytes.Repeat([]byte(" "), line.Indentation-minimumIndentation), line.Content)
		} else {
			br.Renderln()
		}
	}
	parent.InnerText = br.Bytes()

	return true
}

func (p *Parser) Parse() ([]byte, error) {

	// // This regex detects the <x-ref REFERENCE> tags that need special processing
	// reXRef := regexp.MustCompile(`<x-ref +([0-9a-zA-Z-_\.]+) *>`)
	// reCodeBackticks := regexp.MustCompile(`\x60([0-9a-zA-Z-_\.]+)\x60`)

	// // Verbatim sections require special processing to keep their exact format
	// insideVerbatim := false
	// indentationVerbatim := 0

	// Initialize the document structure
	if p.doc.Type == DocumentNode {
		p.Ids = make(map[string]int)
		p.figs = make(map[string]int)
	}

	p.doc.Indentation = -1

	// Process the YAML header if there is one. It should be at the beginning of the file
	err := p.preprocessYAMLHeader()
	if err != nil {
		return nil, err
	}

	// Parse document and generate AST
	p.ParseBlock(p.doc)

	// DEBUG: travel the tree
	theHTML := p.RenderHTML(p.doc)
	//	fmt.Println(string(theHTML))

	return theHTML, nil

}

func (p *Parser) RenderHTML(n *Node) []byte {
	br := &ByteRenderer{}
	for theNode := n.FirstChild; theNode != nil; theNode = theNode.NextSibling {
		theNode.RenderHTML(br)
	}
	theHTML := br.Bytes()
	return theHTML
}

// NewDocumentFromFile reads a file and preprocesses it in memory
func ParseFromFile(fileName string) (*Parser, []byte, error) {

	// Read the whole file into memory
	file, err := os.Open(fileName)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	// Process the file one line at a time, creating a Document object in memory
	linescanner := bufio.NewScanner(file)

	p := &Parser{
		s: linescanner,
		doc: &Node{
			Type: DocumentNode,
		},
	}

	var z *zap.Logger

	debug := true

	// Setup the logging system
	if debug {
		z, err = zap.NewDevelopment()
		if err != nil {
			panic(err)
		}
	} else {
		z, err = zap.NewProduction()
		if err != nil {
			panic(err)
		}
	}

	p.log = z.Sugar()
	defer p.log.Sync()

	fragmentHTML, err := p.Parse()
	if err != nil {
		panic(err)
	}

	return p, fragmentHTML, nil

}

func (p *Parser) preprocessYAMLHeader() error {
	var err error

	s := p.s

	// We need at least one line
	if !s.Scan() {
		return fmt.Errorf("no YAML metadata found")
	}

	// Get a line from the file
	p.currentLine = bytes.Clone(s.Bytes())

	// We accept YAML data only at the beginning of the file
	if !bytes.HasPrefix(p.currentLine, []byte("---")) {
		return fmt.Errorf("no YAML metadata found")
	}

	p.lineCounter++

	// Build a string with all subsequent lines up to the next "---"
	var yamlString strings.Builder
	var endYamlFound bool

	for s.Scan() {

		// Get a line from the file
		p.currentLine = bytes.Clone(s.Bytes())

		// Calculate the line number
		p.lineCounter++

		if bytes.HasPrefix(p.currentLine, []byte("---")) {
			endYamlFound = true
			break
		}

		yamlString.Write(p.currentLine)
		yamlString.WriteString("\n")

	}

	if !endYamlFound {
		return fmt.Errorf("end of file reached but no end of YAML section found")
	}

	// Parse the string that was built as YAML data
	p.Config, err = yaml.ParseYaml(yamlString.String())
	if err != nil {
		p.log.Fatalw("malformed YAML metadata", "error", err)
	}

	config = p.Config

	return nil
}
