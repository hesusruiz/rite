package rite

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/hesusruiz/vcutils/yaml"
)

var stdlog = log.New(os.Stdout, "", 0)

const blank byte = ' '

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

	Ids  map[string]int // To provide numbering of different entity classes
	Figs map[string]int // To provide numbering of figs of different types in the document
	Xref map[string]*Node

	Config *yaml.YAML

	//	log *zap.SugaredLogger
}

func (p *Parser) currentLineNum() int {
	return p.currentLineCounter
}

// SkipBlankLines returns the line number of the first non-blank line,
// starting from the provided line number, or EOF if there are no more blank lines.
// If the start line is non-blank, we return that line.
func (p *Parser) SkipBlankLines() bool {

	for !p.atEOF {

		line := p.ReadLine()

		// line is blank or a comment line
		if line == nil || bytes.HasPrefix(line.Content, []byte("//")) {
			continue
		}

		// If the line is not empty or a comment, we are done
		p.UnreadLine(line)
		return true
	}

	// All lines of the file were processed without finding a blank line
	return false
}

// ReadLine returns one line from the underlying bufio.Scanner.
// It supports one-level backtracking, with the UnreadLine method.
func (p *Parser) ReadLine() *Text {

	// Parsing is stopped when an error is encountered
	if p.lastError != nil {
		return nil
	}

	if p.bufferedLine != nil && p.bufferedPara != nil {
		stdlog.Fatalf("reading a line when both buffered line and paragraph exist")
	}

	// If there is a line alredy buffered, return it
	if p.bufferedLine != nil {
		line := p.bufferedLine
		p.bufferedLine = nil
		return line
	}

	// Retrieve a line and return it in the *Text
	if p.s.Scan() {

		// Get a rawLine from the file
		rawLine := bytes.Clone(p.s.Bytes())
		p.currentLineCounter++

		// Strip blanks at the beginning of the line and calculate indentation based on the difference in length
		// We do not support other whitespace like tabs
		// p.line = bytes.TrimLeft(rawLine, " ")
		p.currentLine = TrimLeft(rawLine, blank)
		if len(p.currentLine) == 0 {
			return nil
		}

		// Calculate indentation by the difference in lengths of the raw vs. the trimmed line
		p.currentIndentation = len(rawLine) - len(p.currentLine)

		// Build the struct to return to caller
		line := &Text{}
		line.LineNumber = p.currentLineNum()
		line.Content = p.currentLine
		line.Indentation = p.currentIndentation

		return line

	}

	// Check if there were other errors apart from EOF
	if err := p.s.Err(); err != nil {
		p.lastError = err
		stdlog.Fatalf("error scanning: %v", err)
	}

	// We have processed all lines of the file
	p.lastError = nil
	p.atEOF = true
	return nil
}

// UnreadLine allows one-level backtracking by buffering one line that was already returned from bufio.Scanner
func (p *Parser) UnreadLine(line *Text) {
	if p.bufferedLine != nil {
		stdlog.Fatalf("UnreadLine: too many calls in line: %d\n", p.currentLineNum())
	}
	p.bufferedLine = line
}

// ReadParagraph is like ReadLine but returns all contiguous lines at the same level of indentation.
// The paragraph starts at the first non-blank line with more indentation than the specified one.
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
				stdlog.Fatalf("no paragraph read, line: %d\n", p.currentLineNum())
			}

			break
		}

		// If the line read is not more indented than the min_indentation, we have finished the paragraph
		if line.Indentation <= min_indentation {
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
	if p.bufferedPara != nil {
		stdlog.Fatalf("UnreadParagraph: too many calls in line: %d\n", p.currentLineNum())
	}
	p.bufferedPara = para
}

// This regex detects the Markdown backticks and double asterisks that need special processing
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

	// Convert the MD '**' to 'b' markup
	if bytes.Contains(lineSt.Content, []byte("*")) {
		lineSt.Content = reMarkdownBold.ReplaceAll(lineSt.Content, []byte("<b>${1}</b>"))
	}

	// Convert the MD '__' to 'i' markup
	if bytes.Contains(lineSt.Content, []byte("_")) {
		lineSt.Content = reMarkdownItalics.ReplaceAll(lineSt.Content, []byte("<i>${1}</i>"))
	}

	// Preprocesslines starting with Markdown headers ('#') and convert to h1, h2, ...
	// We assume that a header starts with the '#' character, no matter what the rest of the line is
	if lineSt.Content[0] == '#' {

		// Trim and count the number of '#'
		plainLine := TrimLeft(lineSt.Content, '#')
		lenPrefix := len(lineSt.Content) - len(plainLine)
		hnum := byte('0' + lenPrefix)

		// Trim the possible whitespace between the '#'s and the text
		plainLine = TrimLeft(plainLine, ' ')

		// Build the new line and store it
		lineSt.Content = append([]byte("<h"), hnum, '>')
		lineSt.Content = append(lineSt.Content, plainLine...)

	}

	// Preprocess Markdown list markers
	// They can start with plain dashes '-' but we support a special format '-(something)'.
	// The 'something' inside parenthesis will be highlighted in the list item
	if bytes.HasPrefix(lineSt.Content, []byte("- ")) || bytes.HasPrefix(lineSt.Content, []byte("-(")) {
		lineSt = p.parseMdList(lineSt)
	}

	return lineSt
}

func getStartSectionTagName(text *Text) []byte {
	// If the tag is less than 3 chars or the node does not start with '<', mark it as a paragraph
	// and do not process it further.
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

// ParseBlock parses the segment of the document that belongs to the block represented by the node.
// The node will have as child nodes all elements that are at the same iundentation
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

		// Set the indentation of the first line of the inner block
		if blockIndentation == -1 {
			blockIndentation = para.Indentation
		}

		// This line belongs to this block
		if para.Indentation == blockIndentation {

			// Create a node for the paragraph as a child of the received node
			child := NewNormalNode(p, p.fileName, para)

			// Section nodes can only be children of other section nodes or of the root Document
			if child.Type == SectionNode && string(child.Id) != "abstract" {
				if parent.Type != DocumentNode && parent.Type != SectionNode {
					stdlog.Fatalf("%s (line %d) error: a section node should be top or child of other section node", parent.p.fileName, child.LineNumber)
				}

				// Increase the level
				child.Level = parent.Level + 1

				// Calculate our sequence number for the parent section
				numSections := 1
				for theNode := parent.FirstChild; theNode != nil; theNode = theNode.NextSibling {
					if theNode.Type == SectionNode {
						numSections++
					}
				}

				child.Outline = fmt.Sprintf("%s%d.", parent.Outline, numSections)

			}

			if child.Type == IncludeNode {
				includeParser, error := ParseIncludeFile(string(child.RestLine), p)
				if error != nil {
					stdlog.Fatalf("%s (line %d) error %v", parent.p.fileName, child.LineNumber, error)
				}
				child = includeParser.doc
			}

			parent.AppendChild(child)

			if child.Type == DiagramNode {
				p.ParseVerbatim(child)
			}
			if child.Type == VerbatimNode {
				p.ParseVerbatim(child)
			}

			// Go to next paragraph
			continue

		}

		// Parse an interior block
		if para.Indentation > blockIndentation {

			// Send the read paragraph back to the parser
			p.UnreadParagraph(para)

			// Sanity check
			// Get the last child processed
			if parent.LastChild == nil {
				stdlog.Fatalf("more indented paragraph without child node, line: %d\n", p.currentLineNum())
			}

			// Parse the block using the child node
			p.ParseBlock(parent.LastChild)
			continue
		}

	}

}

func (p *Parser) parseMdList(lineSt *Text) *Text {
	const simplePrefix = "- "
	const bulletPrefix = "-("
	const additionalPrefix = "-+"
	var r ByteRenderer

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
		r.Render("<li>", restLine)

	} else if bytes.HasPrefix(line, []byte(additionalPrefix)) {

		restLine := line[len(additionalPrefix):]

		// Build the line
		r.Render("<div>", restLine, "</div>")

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
		// bulletTextEncoded := bytes.ReplaceAll(bulletText, []byte(" "), []byte("_"))

		// And the remaining text in the line
		restLine := line[indexRightBracket+1:]

		// Build the line
		// r.Render("<x-li id='", bulletTextEncoded, "'>", "<a href='#", bulletTextEncoded, "' class='selfref'>")
		// r.Render("<b>", bulletText, "</b></a>", restLine)
		// r.Render("<x-li id='", lineNum, "-", bulletText, "'>", restLine)
		r.Render("<x-li id='", bulletText, "'><b>", bulletText, "</b>", restLine)

	}

	l := r.Bytes()
	lineSt.Content = l
	return lineSt

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

		// Process explanation lines (starting with "# -")
		if bytes.HasPrefix(line.Content, []byte("# -")) {

			// Create a node to parse the explanation text
			child := NewVerbatimExplanationNode(p, p.fileName, line)

			// And add it to the AST
			parent.AppendChild(child)

			// Go to process next line
			continue

		}

		// Process normal lines

		// Update minimum indentation if needed
		if line.Indentation < minimumIndentation {
			minimumIndentation = line.Indentation
		}

		// Append the line
		diagContentLines = append(diagContentLines, line)
		lastNonBlankLine = len(diagContentLines)

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

	return true
}

func (p *Parser) Parse() error {

	// Initialize the document structure
	if p.doc.Type == DocumentNode {
		p.Ids = make(map[string]int)
		p.Figs = make(map[string]int)
	}

	p.doc.Indentation = -1

	// Process the YAML header if there is one. It should be at the beginning of the file
	config, err := p.preprocessYAMLHeader()

	// We use the results only if the current parser is not configured already
	if p.Config == nil {
		if err != nil {
			stdlog.Fatalf("malformed YAML metadata: %v\n", err)
		}
		p.Config = config
	}

	// Parse document and generate AST
	p.ParseBlock(p.doc)

	return nil

}

func (p *Parser) RenderHTML() []byte {

	// Get the root node of the document
	n := p.doc

	// Prepare a buffer to receive the rendered bytes
	br := &ByteRenderer{}

	// Render the document node
	n.RenderHTML(br)

	// Return the underlying byte slice
	theHTML := br.Bytes()
	return theHTML
}

// NewDocumentFromFile reads a file and preprocesses it in memory
func ParseAndRenderFromFile(fileName string) (*Parser, []byte, error) {

	// Read the whole file into memory
	file, err := os.Open(fileName)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	// Process the file one line at a time, creating a Document object in memory
	linescanner := bufio.NewScanner(file)

	p := &Parser{
		fileName: fileName,
		s:        linescanner,
		doc: &Node{
			Type: DocumentNode,
		},
	}
	p.Xref = make(map[string]*Node)

	if err := p.Parse(); err != nil {
		panic(err)
	}

	fragmentHTML := p.RenderHTML()

	return p, fragmentHTML, nil

}

// ParseIncludeFile reads the file specified and returns the Parser object containing the AST
func ParseIncludeFile(fileName string, parentParser *Parser) (*Parser, error) {

	// Read the whole file into memory
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Process the file one line at a time, creating a Document object in memory
	linescanner := bufio.NewScanner(file)

	p := &Parser{
		fileName: fileName,
		s:        linescanner,
		doc: &Node{
			Type: DocumentNode,
		},
	}

	// Set values from the parent parser so we update them when parsing this file
	p.Xref = parentParser.Xref
	p.Ids = parentParser.Ids
	p.Figs = parentParser.Figs
	p.Config = parentParser.Config

	// Perform the actual parsing
	if err := p.Parse(); err != nil {
		panic(err)
	}

	// Copy the (possibly) updated values from the current parser to the Parent
	parentParser.Xref = p.Xref
	parentParser.Ids = p.Ids
	parentParser.Figs = p.Figs

	// Mutate the type of root node
	p.doc.Type = IncludeNode
	p.doc.Indentation = 0

	return p, nil

}

func (p *Parser) preprocessYAMLHeader() (*yaml.YAML, error) {
	var err error

	s := p.s

	// We need at least one line
	if !s.Scan() {
		return nil, fmt.Errorf("no YAML metadata found")
	}

	// Get a line from the file
	p.currentLine = bytes.Clone(s.Bytes())

	// We accept YAML data only at the beginning of the file
	if !bytes.HasPrefix(p.currentLine, []byte("---")) {
		return nil, fmt.Errorf("no YAML metadata found")
	}

	p.currentLineCounter++

	// Build a string with all subsequent lines up to the next "---"
	var yamlString strings.Builder
	var endYamlFound bool

	for s.Scan() {

		// Get a line from the file
		p.currentLine = bytes.Clone(s.Bytes())

		// Calculate the line number
		p.currentLineCounter++

		if bytes.HasPrefix(p.currentLine, []byte("---")) {
			endYamlFound = true
			break
		}

		yamlString.Write(p.currentLine)
		yamlString.WriteString("\n")

	}

	if !endYamlFound {
		return nil, fmt.Errorf("end of file reached but no end of YAML section found")
	}

	// Parse the string that was built as YAML data
	config, err := yaml.ParseYaml(yamlString.String())
	if err != nil {
		stdlog.Fatalf("malformed YAML metadata: %v\n", err)
	}

	return config, nil
}
