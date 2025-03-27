package rite

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/hesusruiz/vcutils/yaml"
)

const blank byte = ' '
const commentPrefix = "//"

var stdlog = log.New(os.Stdout, "", 0)

type SyntaxError struct {
	Filename string
	Line     int
	Column   int
	Msg      string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.Filename, e.Line, e.Column, e.Msg)
}

type Parser struct {
	// The source of the document for scanning
	s *bufio.Scanner

	// doc is the document root element.
	doc *Node

	// the file of the name being processed
	fileName string

	// To support one-level backtracking, which is enough for this parser
	bufferedPara *Text
	bufferedLine *Text

	// currentLine is the current source line being processed
	currentLine []byte

	// currentLineCounter is the number of lines processed
	currentLineCounter int

	// currentIndentation is the current currentIndentation
	currentIndentation int

	// This is true when we have read the whole file
	atEOF bool

	// Contains the last error encountered. When this is set, parsing stops
	lastError error

	syntaxErrors []*SyntaxError

	Ids  map[string]int // To provide numbering of different entity classes
	Figs map[string]int // To provide numbering of figs of different types in the document
	Xref map[string]*Node

	Config *yaml.YAML

	Bibdata   *yaml.YAML
	MyBibdata map[string]any
}

func (p *Parser) AddSyntaxError(se *SyntaxError) {
	p.syntaxErrors = append(p.syntaxErrors, se)
}

// NewParser parses a document reading lines from linescanner.
// filename is for logging/tracing purposes.
// The parser has an initial node representing the document (or sub-document) being parsed.
func NewParser(fileName string, linescanner *bufio.Scanner) *Parser {

	p := &Parser{
		fileName: fileName,
		s:        linescanner,
		doc: &Node{
			Type: DocumentNode,
		},
	}

	// Create the maps
	p.Ids = make(map[string]int)
	p.Figs = make(map[string]int)
	p.Xref = make(map[string]*Node)
	p.MyBibdata = make(map[string]any)

	// All nodes have a reference to its parser to access some info
	p.doc.p = p

	// Initialise the config just in case we do not find a suitable one
	p.Config, _ = yaml.ParseYaml("")

	return p

}

var ErrorNoContent = errors.New("no content")

// ParseFromBytes uses a byte array as the source and preprocesses it in memory
// filename is for logging/tracing purposes.
func ParseFromBytes(fileName string, src []byte) (*Parser, error) {

	if len(src) == 0 {
		return nil, ErrorNoContent
	}

	// Create a scanner to process the file one line at a time, creating a Document object in memory
	buf := bytes.NewReader(src)
	linescanner := bufio.NewScanner(buf)

	// Create a new parser for the file
	p := NewParser(fileName, linescanner)

	// Process the YAML header if there is one. It should be at the beginning of the file
	// An error here does not stop parsing.
	err := p.PreprocessYAMLHeader()
	if err != nil {
		log.Println(err.Error())
	}

	// Perform the actual parsing
	if err := p.Parse(); err != nil {
		return nil, err
	}

	return p, nil

}

func (p *Parser) RetrieveBliblioData(baseDir string) *yaml.YAML {

	// Get the bibliography for the references, in the tag "localBiblio"
	// It can be specified in the YAML header or in a separate file in the "localBiblioFile" tag.
	// If both "localBiblio" and "localBiblioFile" exists in the header, only "localBiblio" is used.
	bibData, _ := p.Config.Get("localBiblio")
	if bibData != nil {
		fmt.Println("found local biblio in the front matter")
		p.Bibdata = bibData
		return bibData
	}

	// Try with a file in the same directory as the content file being processed
	dir, _ := filepath.Split(p.fileName)

	fullFileName := filepath.Join(dir, "localbiblio.yaml")
	bibData, err := yaml.ParseYamlFile(fullFileName)
	if err == nil {
		fmt.Println("reading Biblio data from", fullFileName)
		p.Bibdata = bibData
		return bibData
	}

	// Now, we will try in the directory specified by baseDir
	fullFileName = filepath.Join(baseDir, "localbiblio.yaml")
	bibData, err = yaml.ParseYamlFile(fullFileName)
	if err == nil {
		fmt.Println("reading Biblio data from", fullFileName)
		p.Bibdata = bibData
		return bibData
	}

	fmt.Println("bibliography data not found")

	return nil

}

func (p *Parser) RenderBibliography() []byte {

	htmlBuilder := &ByteRenderer{}
	htmlBuilder.Renderln()
	htmlBuilder.Renderln("<section id='References'><h2>References</h2>")
	htmlBuilder.Renderln("<dl>")

	bibdataMap := p.MyBibdata
	for key, v := range bibdataMap {

		e := yaml.New(v)
		title := e.String("title")
		date := e.String("date")
		href := e.String("href")

		htmlBuilder.Renderln("<dt  id='bib_", key, "'>[", key, "]</dt>")
		htmlBuilder.Renderln("<dd>")

		if len(href) > 0 {
			htmlBuilder.Render("<a href='", href, "'>", title, "</a>. ")
		} else {
			htmlBuilder.Render(title, ". ")
		}

		if len(date) > 0 {
			htmlBuilder.Render("Date: ", date, ". ")
		}

		if len(href) > 0 {
			htmlBuilder.Render("URL: <a href='", href, "'>", href, "</a>. ")
		}
		htmlBuilder.Renderln("</dd>")
	}

	htmlBuilder.Renderln("</dl>")
	htmlBuilder.Renderln("</section>")

	return htmlBuilder.Bytes()

}

// ParseFromFile reads a file and preprocesses it in memory
// processYAML indicates if we expect a metadata header in the file.
func ParseFromFile(fileName string, processYAML bool) (*Parser, error) {

	// Open the file
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Process the file one line at a time, creating a Document object in memory
	linescanner := bufio.NewScanner(file)

	// Create a new parser for the file
	p := NewParser(fileName, linescanner)

	// Process the YAML header if there is one. It should be at the beginning of the file
	if processYAML {
		err = p.PreprocessYAMLHeader()
		if err != nil {
			return nil, err
		}
	}

	// Perform the actual parsing
	if err := p.Parse(); err != nil {
		return nil, err
	}

	return p, nil

}

// ParseIncludeFile reads an included file and preprocesses it in memory
// parent is the Node of the parent file where we will include the parsing results.
func (p *Parser) ParseIncludeFile(parent *Node, fileName string, processYAML bool) (*Parser, error) {
	fmt.Println("processing include file", fileName)
	defer fmt.Println("end of include file", fileName)

	// Open the file to process each line one at a time
	file, err := os.Open(fileName)
	if err != nil {
		return nil, fmt.Errorf("opening include file %s: %v", fileName, err)
	}
	defer file.Close()

	// Process the file one line at a time, creating a Document object in memory
	linescanner := bufio.NewScanner(file)

	// Create a new parser for the file
	subParser := NewParser(fileName, linescanner)

	// Set the configuration from the parent parser
	subParser.Config = p.Config

	// Pass the maps for references from the parent parser, so the subparser can update them
	subParser.Ids = p.Ids
	subParser.Figs = p.Figs
	subParser.Xref = p.Xref

	// Perform the actual parsing
	if err := subParser.Parse(); err != nil {
		return nil, fmt.Errorf("parsing include file %s: %v", fileName, err)
	}

	// Update the parent parser with the processed maps
	p.Ids = subParser.Ids
	p.Figs = subParser.Figs
	p.Xref = subParser.Xref

	return subParser, nil

}

func (p *Parser) Parse() error {

	// Parse document and generate AST
	p.ParseBlock(p.doc)

	return nil

}

func (p *Parser) currentLineNum() int {
	return p.currentLineCounter
}

// SkipBlankLines skips blank or comment lines until EOF.
// Returns true if a non-blank line was found, false on EOF.
func (p *Parser) SkipBlankLines() bool {

	for !p.atEOF {

		line := p.ReadLine()

		// Skip blank or comment lines
		if line == nil || bytes.HasPrefix(line.Content, []byte(commentPrefix)) {
			continue
		}

		// If the line is not empty or a comment, we are done
		p.UnreadLine(line)
		return true
	}

	// All lines of the file were processed without finding a non-blank line
	return false
}

// ReadLine returns one line from the underlying bufio.Scanner.
// It supports one-level backtracking, with the UnreadLine method.
func (p *Parser) ReadLine() *Text {

	// Parsing is stopped when an error is encountered
	if p.lastError != nil {
		return nil
	}

	// Sanity check
	if p.bufferedLine != nil && p.bufferedPara != nil {
		// This is a fatal error which can not be recovered
		p.lastError = fmt.Errorf("reading a line when both buffered line and paragraph exist")
		panic(p.lastError)
	}

	// If there is a line alredy buffered, return it
	if p.bufferedLine != nil {
		line := p.bufferedLine
		p.bufferedLine = nil
		return line
	}

	// Retrieve a line and return it
	if p.s.Scan() {

		// Get a rawLine from the file
		// We do a Clone because the result will be modified later during preprocessing
		rawLine := bytes.Clone(p.s.Bytes())

		p.currentLineCounter++

		// Strip blanks at the beginning of the line and calculate indentation
		// We do not support other whitespace like tabs
		p.currentIndentation, p.currentLine = TrimLeft(rawLine, blank)
		p.currentLine = bytes.TrimSpace(p.currentLine)
		if len(p.currentLine) == 0 {
			return nil
		}

		// Build the struct to return to caller
		line := &Text{}
		line.LineNumber = p.currentLineNum()
		line.Content = p.currentLine
		line.Indentation = p.currentIndentation

		return line

	}

	// Check if there were other errors apart from EOF
	if err := p.s.Err(); err != nil {
		// This is a fatal error which can not be recovered
		p.lastError = err
		panic(p.lastError)
	}

	// We have processed all lines of the file
	p.lastError = nil
	p.atEOF = true
	return nil
}

// UnreadLine allows one-level backtracking by buffering one line that was already returned from bufio.Scanner
func (p *Parser) UnreadLine(line *Text) {
	// Sanity check
	if p.bufferedLine != nil {
		// This is a fatal error which can not be recovered
		p.lastError = fmt.Errorf("unreadLine: too many calls in line: %d", p.currentLineNum())
		panic(p.lastError)
	}
	p.bufferedLine = line
}

// ReadParagraph is like ReadLine but returns all contiguous lines at the same level of indentation.
// The paragraph starts at the first non-blank line with more indentation than the specified one.
// A line starting with a block tag is considered a different paragraph, and stops the current paragraph.
func (p *Parser) ReadParagraph(indentation int) *Text {

	// Do nothing if there was a non-recoverable error in parsing
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
	found := p.SkipBlankLines()
	if !found {
		// No blank lines found, must be at EOF
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
				// This is an unrecoverable error, should stop parsing
				p.lastError = fmt.Errorf("no paragraph read, line: %d\n", p.currentLineNum())
				panic(p.lastError)
			}

			break
		}

		// If the line read is not more indented than the min_indentation, we have finished the paragraph
		if line.Indentation < indentation {
			p.UnreadLine(line)
			break
		}

		// A line starting with a block tag is considered a different paragraph
		if para != nil && line.Indentation == para.Indentation {

			if line.Content[0] == '-' {
				p.UnreadLine(line)
				break
			}

			if len(getStartSectionTagName(line)) > 0 {
				p.UnreadLine(line)
				break
			}

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
		br.Renderln(p.currentLine)

	}

	if para != nil {
		// Get the accumulated contents of all lines
		para.Content = br.Bytes()

		// Trim the paragraph to make sure we do not have spurious carriage returns at the end
		para.Content = bytes.TrimSpace(para.Content)

		// Preprocess the paragraph
		para = p.PreprocesLine(para)
	}

	return para

}

// UnreadParagraph allows one-level backtracking by buffering one paragraph that was already returned from bufio.Scanner
func (p *Parser) UnreadParagraph(para *Text) {
	// Sanity check
	if p.bufferedPara != nil {
		// This is a fatal error which can not be recovered
		p.lastError = fmt.Errorf("unreadParagraph: too many calls in line: %d", p.currentLineNum())
		panic(p.lastError)
	}
	p.bufferedPara = para
}

// ReadAnyParagraph reads all contiguous lines with the same indentation, if their indentation
// equal or greater than min_indentation. It skips all blank lines at the beginning.
func (p *Parser) ReadAnyParagraph(min_indentation int) *Text {

	// Do nothing if there was a non-recoverable error in parsing
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

	// Read the first line (can not be blank)
	line := p.ReadLine()

	// Sanity check
	if line == nil {
		// This is a fatal error which can not be recovered
		p.lastError = fmt.Errorf("no paragraph read, line: %d", p.currentLineNum())
		panic(p.lastError)
	}

	// We expect lines with at least the same indentation as specified
	if line.Indentation < min_indentation {
		p.UnreadLine(line)
		return nil
	}

	// Initialize the Paragraph.
	// The indentation of the paragraph is the indentation of the firat line.
	para := &Text{}
	para.LineNumber = p.currentLineNum()
	para.Indentation = line.Indentation

	// Add the contents of the line to the paragraph
	br.Renderln(line.Content)

	// Read and process any possible additional lines
	for line != nil {

		// Read the next line
		line = p.ReadLine()
		if line == nil {
			break
		}

		// If the line has different indentation, the paragraph has finished
		if line.Indentation != para.Indentation {
			p.UnreadLine(line)
			break
		}

		// A line starting with a block tag is considered a different paragraph
		if (line.Content[0] == '-') || (len(getStartSectionTagName(line)) > 0) {
			p.UnreadLine(line)
			break
		}

		// Add the contents of the line to the paragraph
		br.Renderln(line.Content)

	}

	// Get the accumulated contents of all lines
	para.Content = br.Bytes()

	// Trim the paragraph to make sure we do not have spurious carriage returns at the end
	para.Content = bytes.TrimSpace(para.Content)

	para = p.PreprocesLine(para)

	return para

}

func (p *Parser) PeekParagraphFirstLine() *Text {

	// Do nothing if there was a non-recoverable error in parsing
	if p.lastError != nil {
		return nil
	}

	// If there is a paragraph alredy buffered, return it
	if p.bufferedPara != nil {
		return p.bufferedPara
	}

	// Skip all blank lines until EOF or another error
	if !p.SkipBlankLines() {
		return nil
	}

	// Read the first line (can not be blank)
	line := p.ReadLine()
	p.UnreadLine(line)

	return line
}

// This regex detects the Markdown backticks, double asterisks and double underscores that need special processing
var reCodeBackticks = regexp.MustCompile(`\x60(.+?)\x60`)
var reMarkdownBold = regexp.MustCompile(`\*\*(.+?)\*\*`)
var reMarkdownItalics = regexp.MustCompile(`__(.+?)__`)

// PreprocesLine applies some preprocessing to the raw line that was just read from the stream.
// Only preprocessing which is local to the current line can be applied.
func (p *Parser) PreprocesLine(lineSt *Text) *Text {

	// We ignore any line starting with a comment marker: '//'
	if bytes.HasPrefix(lineSt.Content, []byte("//")) {
		return nil
	}

	// We ignore any line starting with an end tag
	if bytes.HasPrefix(lineSt.Content, []byte("</")) {
		return nil
	}

	// Convert backticks to the 'code' tag
	if bytes.Contains(lineSt.Content, []byte("`")) {
		lineSt.Content = reCodeBackticks.ReplaceAll(lineSt.Content, []byte("<code>${1}</code>"))
	}

	// Convert the Markdown '**' to 'b' markup
	if bytes.Contains(lineSt.Content, []byte("*")) {
		lineSt.Content = reMarkdownBold.ReplaceAll(lineSt.Content, []byte("<b>${1}</b>"))
	}

	// Convert the Markdown '__' to 'i' markup
	if bytes.Contains(lineSt.Content, []byte("_")) {
		lineSt.Content = reMarkdownItalics.ReplaceAll(lineSt.Content, []byte("<i>${1}</i>"))
	}

	// Preprocesslines starting with Markdown headers ('#') and convert to h1, h2, ...
	// We assume that a header starts with the '#' character, no matter what the rest of the line is
	if lineSt.Content[0] == '#' {

		// Trim and count the number of '#'
		lenPrefix, plainLine := TrimLeft(lineSt.Content, '#')
		hnum := byte('0' + lenPrefix)

		// Trim the possible whitespace between the '#'s and the text
		_, plainLine = TrimLeft(plainLine, ' ')

		// Build the new line and store it
		lineSt.Content = append([]byte("<h"), hnum, '>')
		lineSt.Content = append(lineSt.Content, plainLine...)

	}

	// Preprocess Markdown list markers
	// They can start with plain dashes '-' but we support a special format '-(something)'.
	// The 'something' inside parenthesis will be highlighted in the list item
	if HasPrefix(lineSt.Content, "- ") || HasPrefix(lineSt.Content, "-(") {
		lineSt = p.parseMdListItem(lineSt)
	}

	return lineSt
}

func getStartSectionTagName(text *Text) []byte {
	// If the tag is less than 3 chars or the node does not start with '<', do not process it further.
	if len(text.Content) < 3 || text.Content[0] != StartHTMLTag {
		return nil
	}

	// Now we know the line starts with a tag '<'

	// Extract the whole tag string between the start and end tags
	// The end bracket is optional if there is no more text in the line after the tag attributes
	indexRightBracket := bytes.IndexByte(text.Content, EndHTMLTag)

	var tagSpec []byte
	if indexRightBracket == -1 {
		tagSpec = text.Content[1:]
	} else {

		// Extract the whole tag spec
		tagSpec = text.Content[1:indexRightBracket]

	}

	// Extract the name of the tag from the tagSpec
	name, _ := ReadTagName(tagSpec)

	return name

}

// NewNode creates a node from the text that is passed.
// The new node is set to the proper type and its attributes populated.
// If the line starts with a proper tag, it is processed and the node is updated accordingly.
func (p *Parser) NewNode(parent *Node, text *Text) *Node {

	n := &Node{}

	// Set the basic fields
	n.p = p
	n.Indentation = text.Indentation
	n.LineNumber = text.LineNumber
	n.RawText = text

	// Process the tag at the beginning of the line, if there is one

	// If the tag is less than 3 chars or the text does not start with '<', mark it as a paragraph
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

		// We did not find the end bracket for the tag, so we treat this as a paragraph
		n.Type = BlockNode
		n.Name = "p"
		n.RestLine = text.Content
		return n

	}

	// Extract the whole tag spec
	tagString := text.Content[1:indexRightBracket]

	// And the remaining text in the line
	n.RestLine = text.Content[indexRightBracket+1:]

	// Extract the name of the tag from the tagSpec
	name, restOfTag := ReadTagName(tagString)

	// If no tag was found, treat the line as a paragraph
	if len(name) == 0 {
		n.Type = BlockNode
		n.Name = "p"
		n.RestLine = text.Content
		return n
	}

	// Set the name of the node with the tag name
	n.Name = string(name)

	// If the tag is not a block element or it is a void one, wrap it in a paragraph and do not process it
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
		fmt.Println("line ", n.LineNumber, text)
	case "x-diagram":
		n.Type = DiagramNode
	case "x-code", "x-example", "pre":
		n.Type = VerbatimNode
	case "x-include":
		n.Type = IncludeNode
	default:
		n.Type = BlockNode
	}

	// Process all the attributes in the tag
	for {

		restOfTag = SkipWhiteSpace(restOfTag)

		// We have finished the loop if there is no more data
		if len(restOfTag) == 0 {
			break
		}

		var attrVal []byte

		switch restOfTag[0] {
		case '#':
			// Shortcut for id="xxxx"

			if len(restOfTag) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}

			// The identifier can be enclosed in single or double quotes if there are spaces
			attrVal, restOfTag = ReadQuotedWords(restOfTag[1:])

			// Only the first id attribute is used, others are ignored
			if len(n.Id) == 0 {
				n.Id = attrVal
			}

		case '.':
			// Shortcut for class="xxxx"

			if len(restOfTag) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}

			// The class name should be a single word
			attrVal, restOfTag = ReadWord(restOfTag[1:])

			// The tag may specify more than one class and all are accumulated
			if len(n.Class) > 0 {
				n.Class = append(n.Class, ' ')
			}
			n.Class = append(n.Class, attrVal...)

		case '@':
			// Shortcut for src="xxxx"

			if len(restOfTag) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}

			// The identifier can be enclosed in single or double quotes if there are spaces
			attrVal, restOfTag = ReadQuotedWords(restOfTag[1:])

			// Only the first attribute is used
			if len(n.Src) == 0 {
				n.Src = attrVal
			}

		case '-':
			// Shortcut for href="xxxx"

			if len(restOfTag) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}

			// The identifier can be enclosed in single or double quotes if there are spaces
			attrVal, restOfTag = ReadQuotedWords(restOfTag[1:])

			// Only the first attribute is used
			if len(n.Href) == 0 {
				n.Href = attrVal
			}

		case ':':
			// Special attribute "type" for item classification and counters

			if len(restOfTag) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}

			// The attribute should be a single word
			attrVal, restOfTag = ReadWord(restOfTag[1:])

			// Only the first attribute is used
			if len(n.Bucket) == 0 {
				n.Bucket = attrVal
			}

		case '=':
			// Special attribute "number" for list items

			if len(restOfTag) < 2 {
				stdlog.Fatalf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", text.LineNumber)
			}

			// The attribute should be a single word
			attrVal, restOfTag = ReadWord(restOfTag[1:])

			// Only the first attribute is used
			if len(n.Number) == 0 {
				n.Number = attrVal
			}

		default:
			// This should be a standard HTML attribute, in 'key=val' format
			var attr Attribute
			attr, restOfTag = ReadTagAttrKey(restOfTag)

			if len(attr.Key) == 0 {
				// Set the tagSpec to nil to break of the loop
				restOfTag = nil
			} else {

				// Treat the most important attributes specially
				switch attr.Key {
				case "id":
					// Set the special Id field if it is not already set
					if len(n.Id) == 0 {
						n.Id = bytes.Clone(attr.Val)
					}
				case "class":
					// More than one class can be specified and and all are accumulated, separated by a spece
					if len(n.Class) > 0 {
						n.Class = append(n.Class, ' ')
					}
					n.Class = append(n.Class, bytes.Clone(attr.Val)...)
				case "src":
					// Only the first attribute is used
					if len(n.Src) == 0 {
						n.Src = bytes.Clone(attr.Val)
					}
				case "href":
					// Only the first attribute is used
					if len(n.Href) == 0 {
						n.Href = bytes.Clone(attr.Val)
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
			stdlog.Panicf("id already used, processing line %d: %s\n", n.LineNumber, n)
		}
		// Include the 'id' in the table and also the text for references
		p.Xref[string(n.Id)] = n
	}

	return n
}

// ParseBlock parses the segment of the document that belongs to the block represented by the node.
// The node will have as child nodes all elements that are at the same indentation
func (p *Parser) ParseBlock(parent *Node) {
	var paragraph *Text

	// Read without consuming the next paragraph, to calculate indentation
	paragraph = p.PeekParagraphFirstLine()

	// If no paragraph, we have reached the end of the block or the file
	if paragraph == nil {
		return
	}

	// Document nodes are virtual and are an exception to indentation
	if parent.Type == DocumentNode {
		// When parsing the block representing the Document, we expect the first paragraph
		// to have the same indentation as the Document node (normally zero)
		if paragraph.Indentation != parent.Indentation {
			return
		}
	} else {
		// For any other block different to Document, we parse only paragraphs more indented than the Block
		if paragraph.Indentation <= parent.Indentation {
			return
		}
	}

	// Read the first paragraph of this Block
	paragraph = p.ReadAnyParagraph(paragraph.Indentation)

	// The first line determines the indentation of this block
	blockIndentation := paragraph.Indentation

	// Process the paragraphs until there is not more in the block
	for {

		// This paragraph belongs to this block
		if paragraph.Indentation == blockIndentation {

			// Create a node for the paragraph
			newNode := p.NewNode(parent, paragraph)

			// If it is a section, calculate its sequence number.
			// The "abstract" section is not numbered.
			if newNode.Type == SectionNode && string(newNode.Id) != "abstract" {

				// Section nodes can only be children of other section nodes or of the root Document
				if parent.Type != DocumentNode && parent.Type != SectionNode {
					// Abort the parsing
					p.lastError = fmt.Errorf("%s (line %d) error: a section node should be top or child of other section node", parent.p.fileName, newNode.LineNumber)
					panic(p.lastError)
				}

				// Increase the level
				newNode.Level = parent.Level + 1

				// Calculate our sequence number for the parent section
				numSections := 1
				for theNode := parent.FirstChild; theNode != nil; theNode = theNode.NextSibling {
					if theNode.Type == SectionNode && string(theNode.Id) != "abstract" {
						numSections++
					}
				}

				newNode.Outline = fmt.Sprintf("%s%d.", parent.Outline, numSections)

			}

			// Process the inclusion of another file at this point
			if newNode.Type == IncludeNode {

				// If the file name specified by the user is relative, it is treated as relative to the location of
				// the file including it, so it should exist either in the same directory of in a subdirectory.
				// TODO: the name can be a URL
				fileName := string(newNode.Src)

				// Get the base name of the parent node
				baseDir, _ := filepath.Split(parent.p.fileName)

				// Get the target file name based on the parent
				targetPath, err := filepath.Rel(baseDir, filepath.Join(baseDir, fileName))

				if err != nil {
					// Abort parsing
					p.lastError = fmt.Errorf("%s (line %d) error: invalid path in include directive: %s - %s", parent.p.fileName, newNode.LineNumber, baseDir, fileName)
					panic(p.lastError)
				}
				fileName = filepath.Join(baseDir, targetPath)

				// Open the file and parse it
				subParser, err := p.ParseIncludeFile(parent, fileName, false)
				if err != nil {
					// Abort parsing
					p.lastError = fmt.Errorf("parsing include file %s: %w", fileName, err)
					panic(p.lastError)
				}

				// Add all top nodes of the included document as childs of the current parent
				ReparentChildren(parent, subParser.doc)

			} else {
				// Add the new node as a child of the parent node
				parent.AppendChild(newNode)

			}

			// If the node is of verbatim type, perform special processing of its content
			if newNode.Type == DiagramNode {
				p.ParseVerbatim(newNode)
			}
			if newNode.Type == VerbatimNode {
				p.ParseVerbatim(newNode)
			}

		}

		// If the paragraph is more indented than the block, it represents an interior block
		if paragraph.Indentation > blockIndentation {

			// Send the read paragraph back to the parser
			p.UnreadParagraph(paragraph)

			// Sanity check: there should be at least a child node of the parent node
			if parent.LastChild == nil {
				// Abort parsing
				p.lastError = fmt.Errorf("more indented paragraph without child node, line: %d", p.currentLineNum())
			}

			// Parse the interior block using the child node as its parent
			p.ParseBlock(parent.LastChild)
		}

		// Check if the next paragraph is less indented, so the block ends
		paragraph = p.PeekParagraphFirstLine()

		// If no paragraph or less indentation, we have reached the end of the block or the file
		if (paragraph == nil) || (paragraph.Indentation < blockIndentation) {
			return
		}

		// Read the next paragraph and loop again
		paragraph = p.ReadAnyParagraph(blockIndentation)

	}

}

// parseMdListItem preprocesses a markdown list item, converting it to an HTML5 list item tag
func (p *Parser) parseMdListItem(lineSt *Text) *Text {
	const simplePrefix = "- "
	const bulletPrefix = "-("
	const additionalPrefix = "-+"
	var htmlBuilder ByteRenderer

	// We receive a list item in Markdown format and we convert to proper HTML

	lineNum := lineSt.LineNumber
	line := lineSt.Content

	// This is to support explanations inside verbatim text, which start with a comment: "# -"
	if bytes.HasPrefix(line, []byte("# -")) {
		line = line[2:]
	}

	// Preprocess Markdown list markers
	// They can start with plain dashes '-' but we support a special format '-(something)'.
	// The 'something' inside parenthesis will be highlighted in the list item
	if bytes.HasPrefix(line, []byte(simplePrefix)) {

		restLine := line[len(simplePrefix):]

		// Build the line
		htmlBuilder.Render("<li>", restLine)

	} else if bytes.HasPrefix(line, []byte(additionalPrefix)) {

		restLine := line[len(additionalPrefix):]

		// Build the line
		htmlBuilder.Render("<div>", restLine, "</div>")

	} else if bytes.HasPrefix(line, []byte(bulletPrefix)) {

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
		bulletTextEncoded := bytes.ReplaceAll(bulletText, []byte(" "), []byte("_"))

		// Build a unique element id based on the encoded bullet text
		var id ByteRenderer
		id.Render(bulletTextEncoded, "_", lineNum)

		// And the remaining text in the line
		restLine := line[indexRightBracket+1:]

		// Build the line
		// r.Render("<x-li id='", bulletTextEncoded, "'>", "<a href='#", bulletTextEncoded, "' class='selfref'>")
		// r.Render("<b>", bulletText, "</b></a>", restLine)
		htmlBuilder.Render("<li id='", id.Bytes(), "'><b>", bulletText, "</b>", restLine)

	}

	l := htmlBuilder.Bytes()
	lineSt.Content = l
	return lineSt

}

func (p *Parser) parseVerbatimExplanation(node *Node) {

	// We receive in node.RawText the unparsed explanation paragraph
	// We convert it into a list item with the proper markup
	// Sanity check
	node.RawText = p.parseMdListItem(node.RawText)

	// Parse the possible inner block
	p.ParseBlock(node)

}

func (p *Parser) ParseVerbatim(parent *Node) error {

	if len(parent.Src) > 0 {
		p.ParseVerbatimIncluded(parent)
		return nil
	}

	// The first line will determine the indentation of the block
	sectionIndent := parent.Indentation
	blockIndentation := -1

	// This will hold the string with the text lines for diagram
	diagContentLines := []*Text{}

	// We are going to calculate the minimum indentation for the whole block.
	// The starting point is a very big value which will be reduced to the correct value during the loop
	minimumIndentation := math.MaxInt

	// This is to keep track of the last non-blank line in the diagram
	// Because of the way we detect the end of the block, there may be spurious blank lines at the end
	lastNonBlankLine := 0

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

		// Process normal lines (those not starting with the special prefix "# -")
		if !bytes.HasPrefix(line.Content, []byte("# -")) {

			// Update minimum indentation if needed
			if line.Indentation < minimumIndentation {
				minimumIndentation = line.Indentation
			}

			// Append the line
			diagContentLines = append(diagContentLines, line)
			lastNonBlankLine = len(diagContentLines)

		} else {

			// Create a node to parse the explanation text
			child := &Node{}
			child.p = p
			parent.AppendChild(child)
			child.Type = ExplanationNode

			// Add the paragraph to the node's paragraph
			child.RawText = line
			// This is really redundant but facilitates life for processing
			// This way the node has all relevant info at the main level
			child.Indentation = line.Indentation
			child.LineNumber = line.LineNumber

			p.parseVerbatimExplanation(child)

		}

		// Go to process next line
		continue

	}

	var br ByteRenderer

	// We will accumulate the content in the InnerText field of the node
	// Loop for all entries until the last one which is non-blank
	for _, line := range diagContentLines[:lastNonBlankLine] {
		if len(line.Content) > 0 {
			br.Renderln(bytes.Repeat([]byte(" "), line.Indentation-minimumIndentation), line.Content)
		} else {
			br.Renderln()
		}
	}

	parent.InnerText = br.Bytes()

	return nil

}

func (p *Parser) ParseVerbatimIncluded(parent *Node) error {

	// If the file name specified by the user is relative, it is treated as relative to the location of
	// the file including it, so it should exist either in the same directory of in a subdirectory.
	// TODO: the name can be a URL
	fileName := string(parent.Src)

	// Get the base name of the file
	baseDir, _ := filepath.Split(p.fileName)

	// Get the target file name
	targetPath, err := filepath.Rel(baseDir, filepath.Join(baseDir, fileName))

	if err != nil {
		stdlog.Fatalf("%s (line %d) error: invalid path in include directive: %s - %s", p.fileName, parent.LineNumber, baseDir, fileName)
	}
	fileName = filepath.Join(baseDir, targetPath)

	// Read the whole file into memory
	fileContents, err := os.ReadFile(fileName)
	if err != nil {
		p.lastError = err
		return err
	}

	parent.InnerText = fileContents

	return nil
}

func (p *Parser) RenderHTML() []byte {

	// Prepare a buffer to receive the rendered bytes
	br := &ByteRenderer{}

	// Travel the parse tree rendering each node
	p.doc.RenderHTML(br)

	// Return the underlying byte slice
	theHTML := br.Bytes()
	return theHTML
}

func (p *Parser) PreprocessYAMLHeader() error {
	var err error

	// Initialise the config just in case we do not find a suitable one
	p.Config, err = yaml.ParseYaml("")
	if err != nil {
		return err
	}

	line := p.PeekParagraphFirstLine()
	if line == nil || len(line.Content) == 0 {
		return fmt.Errorf("empty file")
	}

	// We accept YAML data only at the beginning of the file
	if !bytes.HasPrefix(line.Content, []byte("---")) {
		return fmt.Errorf("no YAML metadata found in the file")
	}

	// Just discard the line
	p.ReadLine()

	// Build a string with all subsequent lines up to the next "---"
	var yamlString strings.Builder
	var endYamlFound bool

	for !p.atEOF {

		line := p.ReadLine()
		if line == nil {
			continue
		}

		// Check for end of YAML section
		if bytes.HasPrefix(line.Content, []byte("---")) {
			endYamlFound = true
			break
		}

		yamlString.WriteString(strings.Repeat(" ", line.Indentation) + string(line.Content))
		yamlString.WriteString("\n")

	}

	frontMatter := yamlString.String()

	if !endYamlFound {
		return fmt.Errorf("end of file reached but no end of YAML section found")
	}

	// Parse the string that was built as YAML data
	p.Config, err = yaml.ParseYaml(frontMatter)
	if err != nil {
		stdlog.Fatalf("malformed YAML metadata: %v\n", err)
	}

	// config = p.Config

	return nil
}
