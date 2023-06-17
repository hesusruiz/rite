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
			log.Fatalf("attemping to write something not a string, int, rune, []byte or byte: %T", s)
		}
	}
}

func (r *ByteRenderer) Renderln(inputs ...any) {
	r.Render(inputs...)
	r.Render('\n')
}

type Paragraph struct {
	Indentation int
	Line        int
	Content     []byte
}

func (para *Paragraph) String() string {
	if para == nil {
		return "<nil><nil><nil>"
	}

	numChars := 10
	if len(para.Content) < numChars {
		numChars = len(para.Content)
	}

	return strings.Repeat(" ", para.Indentation) + string(para.Content[:numChars])
}

type Line struct {
	Indentation int
	Line        int
	Content     []byte
}

type Parser struct {
	// The source of the document for scanning
	s *bufio.Scanner

	// doc is the document root element.
	doc *Node

	bufferedPara *Paragraph
	bufferedLine *Line

	// currentLine is the current raw currentLine
	currentLine []byte

	// lineCounter is the number of lines processed
	lineCounter int

	// currentIndentation is the current currentIndentation
	currentIndentation int

	// This is true when we have read the whole file
	atEOF bool

	lastError error

	// tok is the most recently read token.
	tok Token

	// fragment is whether the parser is parsing a Rite fragment.
	fragment bool

	// context is the context element when parsing a Rite fragment
	context *Node

	ids    map[string]int // To provide numbering of different entity classes
	figs   map[string]int // To provide numbering of figs of different types in the document
	config *yaml.YAML

	log *zap.SugaredLogger
}

func (p *Parser) currentLineNum() int {
	return p.lineCounter - 1
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

	// All lines of the file were processed without finding a balnk line
	return false
}

func (p *Parser) ReadLine() *Line {
	if p.lastError != nil {
		return nil
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

		// Strip blanks at the beginning of the line and calculate indentation based on the difference in length
		// We do not support other whitespace like tabs
		// p.line = bytes.TrimLeft(rawLine, " ")
		p.currentLine = trimLeft(rawLine, blank)
		if len(p.currentLine) == 0 {
			return nil
		}

		p.currentIndentation = len(rawLine) - len(p.currentLine)

		p.lineCounter++
		line := &Line{}
		line.Line = p.currentLineNum()
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

func (p *Parser) UnreadLine(line *Line) {
	p.bufferedLine = line
}

func (p *Parser) ReadParagraph() *Paragraph {
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
	var para *Paragraph

	for {

		line := p.ReadLine()

		if line == nil {
			// Sanity check
			if para == nil {
				log.Fatalf("no paragraph read, line: %d\n", p.currentLineNum())
			}

			break
		}

		// Initialize the Paragraph if this is the first line read
		if para == nil {
			para = &Paragraph{}
			para.Line = p.currentLineNum()
			para.Indentation = line.Indentation
		}

		if line.Indentation != para.Indentation {
			p.UnreadLine(line)
			break
		}

		// Add the contents of the line to the paragraph
		br.Write(p.currentLine)

	}

	// Return the accumulated contents of all lines
	para.Content = br.Bytes()
	return para

}

func (p *Parser) UnreadParagraph(para *Paragraph) {
	p.bufferedPara = para
}

func (p *Parser) parseInteriorBlock(parent *Node) bool {

	// Skip all the blank lines at the beginning of the block
	if !p.SkipBlankLines() {
		log.Printf("EOF reached at line %d", p.lineCounter)
		return false
	}

	// The first line will determine the indentation of the block
	blockIndentation := -1

	for {

		para := p.ReadParagraph()
		if para == nil {
			return false
		}

		if blockIndentation == -1 {
			blockIndentation = para.Indentation
		}

		// This line belongs to this block
		if para.Indentation == blockIndentation {
			// Create a node for the paragraph
			sibling := &Node{}
			parent.AppendChild(sibling)

			// Add the paragraph to the node's paragraph
			sibling.Para = para
			// TODO: this is redundant, will eliminate it later
			sibling.Indentation = blockIndentation

			// Parse the line
			token, err := p.parseLine(para.Content)
			if err != nil {
				log.Fatalln(err)
			}

			// DEBUG
			if token != nil {
				fmt.Printf("%s%s\n", strings.Repeat(" ", blockIndentation), token)
				sibling.Token = token
			}

			// Go to next paragraph
			continue

		}

		// The paragraph is finished if the line has less indentation
		if para.Indentation < blockIndentation {
			p.UnreadParagraph(para)
			return false
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
			p.parseInteriorBlock(parent.LastChild)
			continue
		}

	}

}

func (p *Parser) Parse() error {

	// // This regex detects the <x-ref REFERENCE> tags that need special processing
	// reXRef := regexp.MustCompile(`<x-ref +([0-9a-zA-Z-_\.]+) *>`)
	// reCodeBackticks := regexp.MustCompile(`\x60([0-9a-zA-Z-_\.]+)\x60`)

	// // Verbatim sections require special processing to keep their exact format
	// insideVerbatim := false
	// indentationVerbatim := 0

	// Initialize the document structure
	if p.doc.Type == DocumentNode {
		p.ids = make(map[string]int)
		p.figs = make(map[string]int)
	}

	// Process the YAML header if there is one. It should be at the beginning of the file
	err := p.preprocessYAMLHeader()
	if err != nil {
		return err
	}

	p.parseInteriorBlock(p.doc)
	return nil

}

func skipWhiteSpace(line []byte) []byte {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return line[i:]
		}
	}
	return nil
}

func readWord(line []byte) (word []byte, rest []byte) {

	// If no blank space found, return the whole tagSpec
	indexSpace := bytes.IndexByte(line, ' ')
	if indexSpace == -1 {
		return line, nil
	}

	// Otherwise, return the tag name and the rest of the tag
	word = line[:indexSpace]

	// And the remaining text in the line
	line = line[indexSpace+1:]

	line = skipWhiteSpace(line)
	return word, line

}

func readTagName(tagSpec []byte) (tagName []byte, rest []byte) {
	return readWord(tagSpec)
}

func readRiteAttribute(tagSpec []byte) (Attribute, []byte) {
	attr := Attribute{}

	indexSpace := bytes.IndexByte(tagSpec, ' ')
	if indexSpace == -1 {
		attr.Val = string(tagSpec)
		return attr, nil
	} else {

		// Extract the whole tag spec
		attr.Val = string(tagSpec[:indexSpace])

		// And the remaining text in the line
		tagSpec = tagSpec[indexSpace+1:]

		tagSpec = skipWhiteSpace(tagSpec)
		return attr, tagSpec

	}

}

func readTagAttrKey(tagSpec []byte) (Attribute, []byte) {
	attr := Attribute{}

	if len(tagSpec) == 0 {
		return attr, nil
	}

	workingTagSpec := tagSpec

	// Select the first word, ending on whitespace, '=' or endtag char '/'
	for i, c := range workingTagSpec {
		if c == ' ' || c == '\t' || c == '/' || c == '=' {
			attr.Key = string(workingTagSpec[:i])
			workingTagSpec = workingTagSpec[i:]
			break
		}
		if i == len(workingTagSpec)-1 {
			attr.Key = string(workingTagSpec)
			return attr, nil
		}
	}

	// Return if next character is not the '=' sign
	workingTagSpec = skipWhiteSpace(workingTagSpec)
	if len(workingTagSpec) == 0 || workingTagSpec[0] != '=' {
		return attr, workingTagSpec
	}

	// Skip whitespace after the '=' sign
	workingTagSpec = skipWhiteSpace(workingTagSpec[1:])

	// This must be the quotation mark, or the end
	quote := workingTagSpec[0]

	switch quote {
	case '>':
		return attr, nil

	case '\'', '"':
		workingTagSpec = workingTagSpec[1:]
		for i, c := range workingTagSpec {
			if c == quote {
				attr.Val = string(workingTagSpec)[:i]
				return attr, workingTagSpec[i+1:]
			}
		}
	default:
		fmt.Printf("malformed tag: %s\n", workingTagSpec)
		panic("malformed tag")

	}
	return attr, workingTagSpec
}

// parseLine returns a structure with the tag fields of the tag at the beginning of the line.
// It returns nil and an error if the line does not start with a tag.
func (p *Parser) parseLine(rawLine []byte) (*Token, error) {
	var tagSpec []byte

	// A token needs at least 3 chars
	if len(rawLine) < 3 || rawLine[0] != startHTMLTag {
		return nil, nil
	}

	t := &Token{}

	t.number = p.lineCounter
	t.indentation = p.currentIndentation

	// Extract the whole tag string between the start and end tags
	// The end bracket is optional if there is no more text in the line after the tag attributes
	indexRightBracket := bytes.IndexByte(rawLine, endHTMLTag)
	if indexRightBracket == -1 {
		tagSpec = rawLine[1:]
	} else {

		// Extract the whole tag spec
		tagSpec = rawLine[1:indexRightBracket]

		// And the remaining text in the line
		t.RestLine = rawLine[indexRightBracket+1:]

	}

	name, tagSpec := readTagName(tagSpec)
	sname := string(name)
	t.Data = sname

	if sname == "section" {
		t.Type = SectionToken
	} else if sname == "diagram" {
		t.Type = DiagramToken
	} else if sname == "x-code" {
		t.Type = CodeToken
	} else if sname == "pre" {
		t.Type = PreToken
	} else if sname == "ol" || sname == "ul" {
		t.Type = ListToken
	} else {
		t.Type = ParagraphToken
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
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineCounter)
			}
			// Shortcut for id="xxxx"
			// Only the first id attribute is used, others are ignored
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Id) == 0 {
				t.Id = attrVal
			}

		case '.':
			if len(tagSpec) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineCounter)
			}
			// Shortcut for class="xxxx"
			// The tag may specify more than one class and all are accumulated
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Class) > 0 {
				t.Class = append(t.Class, ' ')
			}
			t.Class = append(t.Class, attrVal...)
		case '@':
			if len(tagSpec) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineCounter)
			}
			// Shortcut for src="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Src) == 0 {
				t.Src = attrVal
			}

		case '-':
			if len(tagSpec) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineCounter)
			}
			// Shortcut for href="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Href) == 0 {
				t.Href = attrVal
			}
		case ':':
			if len(tagSpec) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineCounter)
			}
			// Special attribute "type" for item classification and counters
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Bucket) == 0 {
				t.Bucket = attrVal
			}
		case '=':
			if len(tagSpec) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineCounter)
			}
			// Special attribute "number" for list items
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Number) == 0 {
				t.Number = attrVal
			}
		default:
			// This should be a standard attribute
			var attr Attribute
			attr, tagSpec = readTagAttrKey(tagSpec)
			if len(attr.Key) == 0 {
				tagSpec = nil
			} else {
				t.Attr = append(t.Attr, attr)
			}

		}

	}

	return t, nil
}

// NewDocumentFromFile reads a file and preprocesses it in memory
func ParseFromFile(fileName string) (*Parser, error) {

	// Read the whole file into memory
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
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

	err = p.Parse()
	if err != nil {
		return nil, err
	}

	return p, nil

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
	p.config, err = yaml.ParseYaml(yamlString.String())
	if err != nil {
		p.log.Fatalw("malformed YAML metadata", "error", err)
	}

	return nil
}

func trimLeft(input []byte, c byte) []byte {
	offset := 0
	for len(input) > 0 && input[0] == c {
		input = input[1:]
	}
	if len(input) == 0 {
		return nil
	}
	return input[offset:]
}
