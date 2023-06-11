package rite

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/hesusruiz/vcutils/yaml"
	"go.uber.org/zap"
	"golang.org/x/net/html"
)

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

type Parser struct {
	// doc is the document root element.
	doc *Node

	// line is the current raw line
	line []byte

	// lineNum is the current line number
	lineNum int

	// indentation is the current indentation
	indentation int

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

func (p *Parser) Parse(s *bufio.Scanner) (*Node, error) {

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
		return nil, err
	}

	// We build in memory the parsed document, pre-processing all lines as we read them.
	// This means that in this stage we can not use information that resides later in the file.
	for s.Scan() {

		// Get a rawLine from the file
		rawLine := bytes.Clone(s.Bytes())

		// Strip blanks at the beginning of the line and calculate indentation based on the difference in length
		// We do not support other whitespace like tabs
		p.line = bytes.TrimLeft(rawLine, " ")
		p.indentation = len(rawLine) - len(p.line)

		// If the line is empty we are done
		if len(p.line) == 0 {
			continue
		}

	}

	return nil, nil

}

func skipWhiteSpace(line []byte) []byte {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return line[i:]
		}
	}
	return nil
}

func readWord(tagSpec []byte) (word []byte, rest []byte) {

	// If no blank spece found, return the whole tagSpec
	indexSpace := bytes.IndexByte(tagSpec, ' ')
	if indexSpace == -1 {
		return tagSpec, nil
	}

	// Otherwise, return the tag name and the rest of the tag
	word = tagSpec[:indexSpace]

	// And the remaining text in the line
	tagSpec = tagSpec[indexSpace+1:]

	tagSpec = skipWhiteSpace(tagSpec)
	return word, tagSpec

}

func readTagName(tagSpec []byte) (tagName []byte, rest []byte) {
	return readWord(tagName)
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

	// Select the first word, ending on whitespace, '=' or '>'
	for i, c := range tagSpec {
		switch c {
		case ' ', '\t', '/', '=', '>':
			attr.Key = string(tagSpec[:i])
			break
		}
	}

	// Return if next character is not the '=' sign
	tagSpec = skipWhiteSpace(tagSpec)
	if len(tagSpec) == 0 || tagSpec[0] != '=' {
		return attr, tagSpec
	}

	// Skip whitespace after the '=' sign
	tagSpec = skipWhiteSpace(tagSpec[1:])

	// This must be the quotation mark, or the end
	quote := tagSpec[0]

	switch quote {
	case '>':
		return attr, nil

	case '\'', '"':
		for i, c := range tagSpec[1:] {
			if c == quote {
				attr.Val = string(tagSpec)[1:i]
				return attr, tagSpec[]
			}
		}



		z.pendingAttr[1].start = z.raw.end
		for {
			c := z.readByte()
			if z.err != nil {
				z.pendingAttr[1].end = z.raw.end
				return
			}
			if c == quote {
				z.pendingAttr[1].end = z.raw.end - 1
				return
			}
		}

	}



}


// parseLine returns a structure with the tag fields of the tag at the beginning of the line.
// It returns nil and an error if the line does not start with a tag.
func (p *Parser) parseLine(rawLine []byte) (*Token, error) {
	var tagSpec, restLine []byte

	// A token needs at least 3 chars
	if len(rawLine) < 3 || rawLine[0] != startHTMLTag {
		return nil, nil
	}

	t := &Token{}

	t.number = p.lineNum
	t.indentation = p.indentation

	// Extract the whole tag string between the start and end tags
	// The end bracket is optional if there is no more text in the line after the tag attributes
	indexRightBracket := bytes.IndexByte(rawLine, endHTMLTag)
	if indexRightBracket == -1 {
		tagSpec = rawLine[1:]
	} else {

		// Extract the whole tag spec
		tagSpec = rawLine[1:indexRightBracket]

		// And the remaining text in the line
		restLine = rawLine[indexRightBracket+1:]

	}

	name, tagSpec := readTagName(tagSpec)
	t.Data = string(name)

	offset := 0

	for {

		var attrVal []byte

		switch tagSpec[offset] {
		case '#':
			if len(tagSpec[offset:]) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineNum)
			}
			// Shortcut for id="xxxx"
			// Only the first id attribute is used, others are ignored
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Id) == 0 {
				t.Id = attrVal
			}

		case '.':
			if len(tagSpec[offset:]) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineNum)
			}
			// Shortcut for class="xxxx"
			// The tag may specify more than one class and all are accumulated
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Class) > 0 {
				t.Class = append(t.Class, ' ')
			}
			t.Class = append(t.Class, attrVal...)
		case '@':
			if len(tagSpec[offset:]) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineNum)
			}
			// Shortcut for src="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Src) == 0 {
				t.Src = attrVal
			}

		case '-':
			if len(tagSpec[offset:]) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineNum)
			}
			// Shortcut for href="xxxx"
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Href) == 0 {
				t.Href = attrVal
			}
		case ':':
			if len(tagSpec[offset:]) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineNum)
			}
			// Special attribute "type" for item classification and counters
			// Only the first attribute is used
			attrVal, tagSpec = readWord(tagSpec[1:])
			if len(t.Bucket) == 0 {
				t.Bucket = attrVal
			}
		case '=':
			if len(tagSpec[offset:]) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", p.lineNum)
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
			attr, tagSpec = readRiteAttribute(tagSpec[1:])
			t.Attr = append(t.Attr, attr)
		}

		// We have finished the loop if there is no more data
		if len(tagSpec) == 0 {
			break
		}

	}

	// Decompose in fields separated by white space.
	// The first field is compulsory and is the tag name.
	// There may be other optional attributes: class name and tag id.
	fields := bytes.Fields(tagSpec)

	if len(fields) == 0 {
		return nil, fmt.Errorf("preprocessTagSpec, line %d: error processing Tag, no tag name found in %s", doc.lastLine, rawLine)
	}

	// Store the unprocessed tag
	t.OriginalTag = tagSpec

	// This is the tag name
	t.Tag = fields[0]

	// This will hold the standard attributes
	var standardAttributes [][]byte

	// Process the special shortcut syntax we provide
	for i := 1; i < len(fields); i++ {
		f := fields[i]

		switch f[0] {
		case '#':
			if len(f) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", doc.lastLine)
			}
			// Shortcut for id="xxxx"
			// Only the first id attribute is used, others are ignored
			if len(t.Id) == 0 {
				t.Id = f[1:]
			}
		case '.':
			if len(f) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", doc.lastLine)
			}
			// Shortcut for class="xxxx"
			// The tag may specify more than one class and all are accumulated
			if len(t.Class) > 0 {
				f[0] = ' '
				t.Class = append(t.Class, f...)
			} else {
				t.Class = f[1:]
			}
			// t.Class = f[1:]
		case '@':
			if len(f) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", doc.lastLine)
			}
			// Shortcut for src="xxxx"
			// Only the first attribute is used
			if len(t.Src) == 0 {
				t.Src = f[1:]
			}
		case '-':
			if len(f) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", doc.lastLine)
			}
			// Shortcut for href="xxxx"
			// Only the first attribute is used
			if len(t.Href) == 0 {
				t.Href = f[1:]
			}
		case ':':
			if len(f) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", doc.lastLine)
			}
			// Special attribute "type" for item classification and counters
			// Only the first attribute is used
			if len(t.Bucket) == 0 {
				t.Bucket = f[1:]
			}
		case '=':
			if len(f) < 2 {
				return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", doc.lastLine)
			}
			// Special attribute "number" for list items
			// Only the first attribute is used
			if len(t.Number) == 0 {
				t.Number = f[1:]
			}
		default:
			// This should be a standard attribute
			standardAttributes = append(standardAttributes, f)
		}
	}

	t.StdFields = bytes.Join(standardAttributes, []byte(" "))

	// The rest of the input line after the tag is available in the "restLine" element
	t.RestLine = restLine

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
	p.line = bytes.Clone(s.Bytes())

	// Calculate the line number
	p.lineNum = p.lineNum + 1

	// We accept YAML data only at the beginning of the file
	if !bytes.HasPrefix(p.line, []byte("---")) {
		return fmt.Errorf("no YAML metadata found")
	}

	// Build a string with all subsequent lines up to the next "---"
	var yamlString strings.Builder
	var endYamlFound bool

	for s.Scan() {

		// Get a line from the file
		p.line = bytes.Clone(s.Bytes())

		// Calculate the line number
		p.lineNum = p.lineNum + 1

		if bytes.HasPrefix(p.line, []byte("---")) {
			endYamlFound = true
			break
		}

		yamlString.Write(p.line)
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
