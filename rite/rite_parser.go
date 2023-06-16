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
	"golang.org/x/net/html"
)

const blank byte = ' '

func tryHtml() {

	s := `<p>Links:</p><ul><li><a href="foo">Foo</a><li><a href="/bar/baz">BarBaz</a></ul>`
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		log.Fatal(err)
	}
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					fmt.Println(a.Val)
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

}

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
	r.Render(inputs, '\n')
}

type Parser struct {
	// doc is the document root element.
	doc *Node

	// currentLine is the current raw currentLine
	currentLine []byte

	// currentLineNum is the current line number
	currentLineNum int

	// currentIndentation is the current currentIndentation
	currentIndentation int

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

// ReadParagraph reads all contiguous lines from the input with the same indentation.
// A line with greater indentation is considered content of an inner block of a section started by the paragraph.
// A line with less indentation is considered content of the parent section.
func (p *Parser) ReadParagraph(s *bufio.Scanner) []byte {

	// Get a rawLine from the file
	rawLine := bytes.Clone(s.Bytes())

	// Strip blanks at the beginning of the line and calculate indentation based on the difference in length
	// We do not support other whitespace like tabs
	// p.line = bytes.TrimLeft(rawLine, " ")
	p.currentLine = trimLeft(rawLine, blank)
	p.currentIndentation = len(rawLine) - len(p.currentLine)

	// If the line is empty we are done
	if len(p.currentLine) == 0 {
		p.currentLineNum++
		p.currentLine = nil
		return nil
	}

	for s.Scan() {

		// Get a rawLine from the file
		rawLine := bytes.Clone(s.Bytes())

		// Strip blanks at the beginning of the line and calculate indentation based on the difference in length
		// We do not support other whitespace like tabs
		// p.line = bytes.TrimLeft(rawLine, " ")
		p.currentLine = trimLeft(rawLine, blank)
		p.currentIndentation = len(rawLine) - len(p.currentLine)

		// If the line is empty we are done
		if len(p.currentLine) == 0 {
			p.currentLineNum++
			p.currentLine = nil
			return nil
		}

	}
	return nil
}

func (p *Parser) Parse(s *bufio.Scanner) error {

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
	err := p.preprocessYAMLHeader(s)
	if err != nil {
		return err
	}

	var br ByteRenderer

	// We build in memory the parsed document, pre-processing all lines as we read them.
	// This means that in this stage we can not use information that resides later in the file.
	for s.Scan() {

		// Start with an empty buffer
		br.Reset()

		// Get a rawLine from the file
		rawLine := bytes.Clone(s.Bytes())

		// Strip blanks at the beginning of the line and calculate indentation based on the difference in length
		// We do not support other whitespace like tabs
		// p.line = bytes.TrimLeft(rawLine, " ")
		p.currentLine = trimLeft(rawLine, blank)
		p.currentIndentation = len(rawLine) - len(p.currentLine)

		// Read all lines with same indentation

		// If the line is empty we are done
		if len(p.currentLine) == 0 {
			p.currentLineNum++
			continue
		}

		br.Renderln(p.currentLine)

		// Create and append a new node
		n := &Node{}
		p.doc.AppendChild(n)

		t, err := p.parseLine(p.currentLine)
		if err != nil {
			log.Fatalf("error in parseLine (%d): %v\n", p.currentLineNum, err)
		}

		if t != nil {
			fmt.Printf("line %d: %v\n", p.currentLineNum+1, t.Data)
		}

		p.currentLineNum++

	}

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

	t.number = p.currentLineNum
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
	t.Data = string(name)

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
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.currentLineNum)
			}
			// Shortcut for id="xxxx"
			// Only the first id attribute is used, others are ignored
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Id) == 0 {
				t.Id = attrVal
			}

		case '.':
			if len(tagSpec) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.currentLineNum)
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
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.currentLineNum)
			}
			// Shortcut for src="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Src) == 0 {
				t.Src = attrVal
			}

		case '-':
			if len(tagSpec) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.currentLineNum)
			}
			// Shortcut for href="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Href) == 0 {
				t.Href = attrVal
			}
		case ':':
			if len(tagSpec) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.currentLineNum)
			}
			// Special attribute "type" for item classification and counters
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Bucket) == 0 {
				t.Bucket = attrVal
			}
		case '=':
			if len(tagSpec) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.currentLineNum)
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
func ParseFromFile(fileName string) (*Node, error) {

	// Read the whole file into memory
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Process the file one line at a time, creating a Document object in memory
	linescanner := bufio.NewScanner(file)

	p := &Parser{
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

	return p.Parse(linescanner)
}

func (p *Parser) preprocessYAMLHeader(s *bufio.Scanner) error {
	var err error

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

	p.currentLineNum++

	// Build a string with all subsequent lines up to the next "---"
	var yamlString strings.Builder
	var endYamlFound bool

	for s.Scan() {

		// Get a line from the file
		p.currentLine = bytes.Clone(s.Bytes())

		// Calculate the line number
		p.currentLineNum++

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
