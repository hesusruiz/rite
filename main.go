package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hesusruiz/vcutils/yaml"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
)

// Document represents a parsed document
type Document struct {
	sb           strings.Builder
	lines        []string       // The lines of the file. We use line numbers to provide meaningful error messages
	indentations []int          // The indentation for each line in the 'lines' array
	ids          map[string]int // To provide numbering of different entity classes
	figs         map[string]int // To provide numbering of figs of different types in the document
	log          *zap.SugaredLogger
	config       *yaml.YAML
}

var debug bool

const startTag = '{'
const endTag = '}'
const startHTMLTag = '<'
const endHTMLTag = '>'

var voidElements = []string{"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr"}
var noSectionElements = []string{
	"b", "i", "hr", "em", "strong", "small", "s",
}
var headingElements = []string{"h1", "h2", "h3", "h4", "h5", "h6"}

var endTagFor = map[rune]rune{
	startTag:     endTag,
	startHTMLTag: endHTMLTag,
}

// // HTML element categories
// var headingCategory = []string{"h1", "h2", "h3", "h4", "h5", "h6"}
// var sectioningCategory = []string{"article", "aside", "nav", "section"}
// var inlineCategory = []string{
// 	"p", "b", "i", "hr", "a", "em", "strong", "small", "s",
// }

type Heading struct {
	subheadings []*Heading
}

// NewDocument parses the input one line at a time, preprocessing the lines and building
// a parsed document ready to be processed
func NewDocument(s *bufio.Scanner, logger *zap.SugaredLogger) *Document {
	re := regexp.MustCompile(`<x-ref +([0-9a-zA-Z-_\.]+) *>`)

	insideVerbatim := false
	indentationVerbatim := 0

	// Create and initialize the document structure
	doc := &Document{}
	doc.lines = []string{}
	doc.ids = make(map[string]int)
	doc.figs = make(map[string]int)
	doc.log = logger

	outline := []*Heading{}
	previousHeading := "h1"

	// Pre-process all lines as we read them
	// This means that we can not use information that resides later in the file
	for s.Scan() {

		// Get a rawLine from the file
		rawLine := s.Text()

		// Calculate its indentation
		line := strings.TrimLeft(rawLine, " ")
		indentation := len(rawLine) - len(line)

		// Trim possible space to make blank lines have zero legth
		line = strings.TrimSpace(line)

		// Calculate the line number
		lineNum := len(doc.lines)

		// Add the line to the array
		doc.lines = append(doc.lines, line)

		// Add the indentation
		doc.indentations = append(doc.indentations, indentation)

		// Preprocess the line if not a blank one
		if len(doc.lines[lineNum]) > 0 {

			// Special processing for verbatim areas.
			if insideVerbatim {
				// Do not process the line if we are still inside a verbatim area
				if indentation > indentationVerbatim {
					continue
				}
				// Check if we exited the verbatim area
				if indentation <= indentationVerbatim {
					insideVerbatim = false
				}
			}

			// Check if we enter into a verbatim area
			if strings.HasPrefix(doc.lines[lineNum], "<pre") {
				insideVerbatim = true
				indentationVerbatim = indentation
			}

			// Preprocess the special <x-ref> tag
			doc.lines[lineNum] = string(re.ReplaceAll([]byte(doc.lines[lineNum]), []byte("<a href=\"#${1}\" class=\"xref\">[${1}]</a>")))

			// Preprocess Markdown headers ('#') and convert to h1, h2, ...
			if doc.lines[lineNum][0] == '#' {

				// Trim and count the number of '#'
				plainLine := strings.TrimLeft(doc.lines[lineNum], "#")
				lenPrefix := len(doc.lines[lineNum]) - len(plainLine)

				switch lenPrefix {
				case 1:
					doc.lines[lineNum] = strings.Replace(doc.lines[lineNum], "#", "<h1>", 1)
				case 2:
					doc.lines[lineNum] = strings.Replace(doc.lines[lineNum], "##", "<h2>", 1)
				case 3:
					doc.lines[lineNum] = strings.Replace(doc.lines[lineNum], "###", "<h3>", 1)
				case 4:
					doc.lines[lineNum] = strings.Replace(doc.lines[lineNum], "####", "<h4>", 1)
				case 5:
					doc.lines[lineNum] = strings.Replace(doc.lines[lineNum], "#####", "<h5>", 1)
				}

			}

			// Preprocess Markdown list markers
			if strings.HasPrefix(doc.lines[lineNum], "- ") {

				doc.lines[lineNum] = strings.Replace(doc.lines[lineNum], "- ", "<li>", 1)

			} else if strings.HasPrefix(doc.lines[lineNum], "-(") {

				line := doc.lines[lineNum]

				// Get the end ')'
				indexRightBracket := strings.IndexRune(line, ')')
				if indexRightBracket == -1 {
					doc.log.Fatalw("no closing ) in list bullet", "line", lineNum)
				}

				// Extract the whole tag spec
				bulletText := line[2:indexRightBracket]
				bulletText = strings.ReplaceAll(bulletText, " ", "%20")

				// And the remaining text in the line
				restLine := line[indexRightBracket+1:]

				// Update the line in the document
				doc.lines[lineNum] = "<li =" + bulletText + ">" + restLine

			}

			// Preprocess tags if they are at the beginning of the line
			if startsWithTag(doc.lines[lineNum]) {
				tagFields := doc.preprocessTagSpec(lineNum)

				// Preprocess tags with ID fields so they can be referenced later
				// We also keep a counter so they can be numbered in the final HTML
				id := tagFields["id"]
				if len(id) > 0 {

					// If the user specified the "type" attribute, we use its value as a classification bucket for numbering
					typ := tagFields["type"]
					if len(typ) == 0 {
						// Otherwise, we use the name of the tag as a classification bucket
						typ = tagFields["tag"]
					}

					// As an example, if the user does not specify anything, all <figures> with an id will be in the
					// same bucket and the counter will be incremented for each figure. But the user may differentiate
					// figures with images from the ones with tables (for example). She can use the special attribute
					// like this: '<figure #picture1 :photos>' or for tables '<figure #tablewithgrowthrate :tables> The
					// names of the buckets (the string after the ':') can be any, and there may be as many as needed.

					// We don't allow duplicate id
					if doc.ids[id] > 0 {
						doc.log.Fatalw("id already used", "line", lineNum, "id", id)
					}

					// Increment the number of elements in this bucket
					doc.figs[typ] = doc.figs[typ] + 1
					// And set the current value of the counter for this id.
					doc.ids[id] = doc.figs[typ]

					// // If the special string '{#my.num}' appears in the line, we can perform the replacement.
					// line = strings.Replace(line, "{#h.num}", fmt.Sprint(b.figs[typ]), 1)

				}

				// Preprocess headings (h1, h2, h3, ...), creating the tree of content
				// We accept a heading of a given level only if it is the same level, one more or one less than
				// the previously encountered heading
				tagName, htmlTag, rest := doc.processTagSpec(lineNum)
				if contains(headingElements, tagName) {
					if !strings.Contains(htmlTag, "no-num") {

						newHeading := &Heading{}
						switch tagName {
						case "h1":
							outline = append(outline, newHeading)
							doc.lines[lineNum] = fmt.Sprintf("%v<span class='secno'>%v</span> %v", htmlTag, len(outline), rest)
							previousHeading = "h1"
						case "h2":
							if previousHeading != "h1" && previousHeading != "h2" && previousHeading != "h3" {
								doc.log.Fatalf("line %v: adding '%v' but previous heading was '%v'\n", len(doc.lines)+1, tagName, previousHeading)
							}
							if len(outline) == 0 {
								doc.log.Fatalf("line %v: adding '%v' but no 'h1' exists\n", len(doc.lines)+1, tagName)
							}
							l1 := outline[len(outline)-1]
							l1.subheadings = append(l1.subheadings, newHeading)
							doc.lines[lineNum] = fmt.Sprintf("%v<span class='secno'>%v.%v</span> %v", htmlTag, len(outline), len(l1.subheadings), rest)
							previousHeading = "h2"
						case "h3":
							if previousHeading != "h2" && previousHeading != "h3" && previousHeading != "h4" {
								doc.log.Fatalf("line %v: adding '%v' but previous heading was '%v'\n", len(doc.lines)+1, tagName, previousHeading)
							}
							if len(outline) == 0 {
								doc.log.Fatalf("line %v: adding '%v' but no 'h1' exists\n", len(doc.lines)+1, tagName)
							}
							l1 := outline[len(outline)-1]
							if len(l1.subheadings) == 0 {
								doc.log.Fatalf("line %v: adding '%v' but no 'h2' exists\n", len(doc.lines)+1, tagName)
							}
							l2 := l1.subheadings[len(l1.subheadings)-1]
							l2.subheadings = append(l2.subheadings, newHeading)
							doc.lines[lineNum] = fmt.Sprintf("%v<span class='secno'>%v.%v.%v</span> %v", htmlTag, len(outline), len(l1.subheadings), len(l1.subheadings), rest)
							previousHeading = "h3"

						}
					}

				}

			}

		}

	}

	// Check if there was any error
	err := s.Err()
	if err != nil {
		doc.log.Errorw("error scanning the input file", "err", err)
	}

	return doc

}

func (doc *Document) preprocessYAMLHeader() int {
	var err error

	// We accept YAML data only at the beginning of the file
	if !strings.HasPrefix(doc.lines[0], "---") {
		doc.log.Debugln("no YAML metadata found")
		return 0
	}

	var i int
	var yamlString strings.Builder
	for i = 1; i < len(doc.lines); i++ {
		if strings.HasPrefix(doc.lines[i], "---") {
			i++
			break
		}

		yamlString.WriteString(doc.lines[i])
		yamlString.WriteString("\n")

	}

	doc.config, err = yaml.ParseYaml(yamlString.String())
	if err != nil {
		doc.log.Fatalw("malformed YAML metadata", "error", err)
	}

	return i
}

func NewDocumentFromFile(fileName string, logger *zap.SugaredLogger) *Document {

	// Read the simple template
	file, err := os.Open(fileName)
	if err != nil {
		logger.Fatalln(err)
	}
	defer file.Close()

	linescanner := bufio.NewScanner(file)

	return NewDocument(linescanner, logger)

}

func (doc *Document) SetLogger(logger *zap.SugaredLogger) {
	doc.log = logger
}

func contains(set []string, tagName string) bool {
	for _, el := range set {
		if tagName == el {
			return true
		}
	}
	return false
}

func isVoidElement(tagName string) bool {
	for _, el := range voidElements {
		if tagName == el {
			return true
		}
	}
	return false
}

func isNoSectionElement(tagName string) bool {
	for _, el := range noSectionElements {
		if tagName == el {
			return true
		}
	}
	return false
}

const EOF = -1

// AtEOF returns true if the line number is beyond the end of file
func (doc *Document) AtEOF(lineNum int) bool {
	return lineNum == EOF || lineNum >= len(doc.lines)
}

// startsWithTag returns true if the line starts with one of the possible start tags
func startsWithTag(line string) bool {
	// Check both standard HTML tag and our special tag
	return line[0] == startTag || line[0] == startHTMLTag
}

// startsWithHeaderTag returns true if the line starts with <h1>, <h2>, ...
func (doc *Document) startsWithHeaderTag(lineNum int) bool {

	line := doc.lines[lineNum]

	if len(line) < 4 {
		return false
	}
	if line[0] == '<' && line[1] == 'h' {
		hnum := line[2]
		if hnum == '1' || hnum == '2' || hnum == '3' || hnum == '4' || hnum == '5' || hnum == '6' {
			return true
		}
	}

	return false
}

// startsWithSectionTag returns true if the line:
//
//	starts either with the HTML tag ('<') or our special tag
//	and it is followed by a blank line or a line which is more indented
func (doc *Document) startsWithSectionTag(lineNum int) bool {

	// thisIndentation := doc.Indentation(lineNum)

	// Decompose the tag into its elements
	tagFields := doc.preprocessTagSpec(lineNum)

	// Return false if there is no tag or it is in the sets that we know should not start a section
	// For example, void elements
	if tagFields == nil || isNoSectionElement(tagFields["tag"]) || contains(voidElements, tagFields["tag"]) {
		return false
	}

	// if lineNum+1 < len(doc.lines) {
	// 	return len(doc.lines[lineNum+1]) == 0 || doc.Indentation(lineNum+1) > thisIndentation
	// }

	return true

	// // Skip all the blank lines
	// nextLineNum := doc.skipBlankLines(lineNum + 1)

	// // Check if next line is more indented (if we are not yet at EOF)
	// if nextLineNum < len(doc.lines) {
	// 	return doc.Indentation(nextLineNum) > thisIndentation
	// }

	// return false
}

func (doc *Document) Indentation(lineNum int) int {
	return doc.indentations[lineNum]
}

// skipBlankLines returns the line number of the first non-blank line,
// starting from the provided line number, or EOF if there are no more blank lines.
// If the start line is non-blank, we return that line.
func (doc *Document) skipBlankLines(lineNumber int) int {
	var trimmedLine string

	for i := lineNumber; i < len(doc.lines); i++ {

		// Trim all blanks to see if the line is a blank line
		trimmedLine = strings.TrimSpace(doc.lines[i])

		// Return if non-blank
		if len(trimmedLine) > 0 {
			return i
		}

	}

	// Return the size of the file (one more than the last line number)
	// This is used as an indication that we are at End of File
	return len(doc.lines)
}

func (doc *Document) printPreprocessStats() {
	fmt.Printf("Number of lines: %v\n", len(doc.lines))
	fmt.Println()
	fmt.Printf("Number of ids: %v\n", len(doc.ids))
	for k, v := range doc.figs {
		fmt.Printf("  %v: %v\n", k, v)
	}
}

func (doc *Document) ToHTML() string {
	// Start processing the main block
	i := doc.preprocessYAMLHeader()
	doc.ProcessBlock(i)
	return doc.postProcess()
}

// postProcess performs any process that can only be done after the whole document has been processed,
// like cross references between sections.
// It returns the final document as a string
func (doc *Document) postProcess() string {

	// Get the name of the template or the default name
	templateName := doc.config.String("template", "assets/output_template.html")

	// Build the full document with the template
	tmpl, err := os.ReadFile(templateName)
	if err != nil {
		doc.log.Fatalw("error reading template", "error", err, "name", templateName)
		panic(err)
	}
	html := string(bytes.Replace(tmpl, []byte("HERE_GOES_THE_CONTENT"), []byte(doc.sb.String()), 1))

	replacePairs := []string{}
	// Calculate the counters placeholders that we have to replace by their actual values
	for id, v := range doc.ids {
		replacePairs = append(replacePairs, "{#"+id+".num}", fmt.Sprint(v))
	}

	// The title in the metadata
	title := doc.config.String("title", "title")
	replacePairs = append(replacePairs, "{#title}", title)

	// Perform the counter substitution on the string representing the document
	replacer := strings.NewReplacer(replacePairs...)
	html = replacer.Replace(html)

	return html
}

// preprocessTagSpec returns a map with the tag fields, or nil if not a tag
func (doc *Document) preprocessTagSpec(rawLineNum int) (tagFields map[string]string) {
	var tagSpec, restLine string

	rawLine := doc.lines[rawLineNum]

	// Sanity check
	if !startsWithTag(rawLine) {
		return nil
	}

	tagFields = make(map[string]string)

	// Trim the start and end brackets: '{' and '}'
	// The end bracket is optional if there is no more text in the line after the tag attributes
	indexRightBracket := strings.IndexRune(rawLine, endTagFor[rune(rawLine[0])])
	if indexRightBracket == -1 {
		tagSpec = rawLine[1:]
		restLine = ""
	} else {

		// Extract the whole tag spec
		tagSpec = rawLine[1:indexRightBracket]

		// And the remaining text in the line
		restLine = rawLine[indexRightBracket+1:]

	}

	// Decompose in fields separated by blank spece.
	// The first field is compulsory and is the tag name.
	// There may be other optional attributes: class name and tag id.
	fields := strings.Fields(tagSpec)

	if len(fields) == 0 {
		doc.log.Fatalf("line %v, error processing Tag, no tag name found in %v", rawLineNum+1, doc.lines[rawLineNum])
	}

	tagFields["tag"] = fields[0]
	tagSpec = strings.Replace(tagSpec, fields[0], "", 1)

	// Process the special shortcut syntax we provide
	for i := 1; i < len(fields); i++ {
		f := fields[i]

		switch f[0] {
		case '#':
			// Shortcut for id="xxxx"
			if len(f) < 2 {
				doc.log.Fatalf("line %v, Length of attributes must be greater than 1", rawLineNum)
			}
			tagFields["id"] = f[1:]
			tagSpec = strings.Replace(tagSpec, f, "", 1)
		case '.':
			// Shortcut for class="xxxx"
			if len(f) < 2 {
				doc.log.Fatalf("line %v, Length of attributes must be greater than 1", rawLineNum)
			}
			tagFields["class"] = f[1:]
			tagSpec = strings.Replace(tagSpec, f, "", 1)
		case '@':
			// Shortcut for src="xxxx"
			if len(f) < 2 {
				doc.log.Fatalf("line %v, Length of attributes must be greater than 1", rawLineNum)
			}
			tagFields["src"] = f[1:]
			tagSpec = strings.Replace(tagSpec, f, "", 1)
		case '-':
			// Shortcut for href="xxxx"
			if len(f) < 2 {
				doc.log.Fatalf("line %v, Length of attributes must be greater than 1", rawLineNum)
			}
			tagFields["href"] = f[1:]
			tagSpec = strings.Replace(tagSpec, f, "", 1)
		case ':':
			// Special attribute "type" for item classification and counters
			if len(f) < 2 {
				doc.log.Fatalf("line %v, Length of attributes must be greater than 1", rawLineNum)
			}
			tagFields["type"] = f[1:]
			tagSpec = strings.Replace(tagSpec, f, "", 1)
		case '=':
			// Special attribute "number" for list items
			if len(f) < 2 {
				doc.log.Fatalf("line %v, Length of attributes must be greater than 1", rawLineNum)
			}
			tagFields["number"] = f[1:]
			tagSpec = strings.Replace(tagSpec, f, "", 1)
		}
	}

	// The remaining string (if any) inside the tag are standard HTM attributes set by the user
	// We do not process them and expose as they are in the "stdFields" element
	stdFields := strings.TrimSpace(tagSpec)
	if len(stdFields) > 0 {
		tagFields["stdFields"] = stdFields
	}

	// The rest of the input line after the tag is available in the "restLine" element
	tagFields["restLine"] = restLine

	return tagFields
}

func (doc *Document) buildTagPresentation(rawLineNum int, tagFields map[string]string) (tagName string, htmlTag string, rest string) {

	// Sanity check
	if tagFields == nil {
		doc.log.Fatalln("tagFields is nil")
	}

	tagName = tagFields["tag"]
	htmlTag = fmt.Sprintf("<%v", tagName)

	// Build the HTML start tag
	for k, v := range tagFields {

		if k != "tag" && k != "stdFields" && k != "restLine" {
			htmlTag = htmlTag + fmt.Sprintf(` %v="%v"`, k, v)
		}
		if k == "stdFields" {
			htmlTag = htmlTag + fmt.Sprintf(` %v`, v)
		}

	}
	htmlTag = htmlTag + ">"

	restLine := tagFields["restLine"]
	return tagName, htmlTag, restLine

}

// processTagSpec builds the tag for presentation, and returns:
// - tagName is the plain tag name, like "div", "h1", "table".
// - htmlTag is the full start tag, as in <div id="the_id" class="note">
// - rest is the rest of the input line after the tag
func (doc *Document) processTagSpec(rawLineNum int) (tagName string, htmlTag string, rest string) {

	// Get a map with the tag components
	tagFields := doc.preprocessTagSpec(rawLineNum)

	// Sanity check
	if tagFields == nil {
		doc.log.Fatalw("no tag in line", "line", rawLineNum, "l", doc.lines[rawLineNum])
	}

	return doc.buildTagPresentation(rawLineNum, tagFields)

}

// processParagraph reads all contiguous lines of a block, unless it encounters some special tag at the beginning
func (doc *Document) processParagraph(startLineNum int) int {
	var tagName, htmlTag string
	var i int
	var startLine string
	var nextLineNum int

	// We process all contiguous lines without taking into account its indentation
	rawLine := doc.lines[startLineNum]

	if startsWithTag(rawLine) {

		// Process the paragraph with attributes
		tagName, htmlTag, startLine = doc.processTagSpec(startLineNum)

		if isNoSectionElement(tagName) {
			// A normal paragraph without any command
			startLine = rawLine
			nextLineNum = startLineNum + 1
			tagName = "p"

			// Write the first line
			doc.sb.WriteString(fmt.Sprintf("%v<%v>%v\n", strings.Repeat(" ", doc.Indentation(startLineNum)), tagName, startLine))

		} else {
			// Write the first line
			doc.sb.WriteString(fmt.Sprintf("%v%v%v\n", strings.Repeat(" ", doc.Indentation(startLineNum)), htmlTag, startLine))

			// Point to the next line in the block (if there are any)
			nextLineNum = startLineNum + 1

		}

	} else {

		// A raw text which starts without any tag
		startLine = rawLine
		nextLineNum = startLineNum + 1
		tagName = "p"

		// Write the first line
		doc.sb.WriteString(fmt.Sprintf("%v<%v>%v\n", strings.Repeat(" ", doc.Indentation(startLineNum)), tagName, startLine))
	}

	// Process the rest of contiguous lines in the block, writing them without any processing
	for i = nextLineNum; i < len(doc.lines); i++ {
		line := doc.lines[i]
		if len(line) > 0 {
			doc.sb.WriteString(fmt.Sprintf("%v%v\n", strings.Repeat(" ", doc.Indentation(i)), line))
		} else {
			break
		}
	}

	// Write the end tag
	if isVoidElement(tagName) {
		// HTML spec says no end tag should be used
		doc.sb.WriteString(fmt.Sprintln())
	} else {
		doc.sb.WriteString(fmt.Sprintf("%v</%v>\n", strings.Repeat(" ", doc.Indentation(startLineNum)), tagName))
	}

	// Return the next line to process
	return i

}

// processHeaderParagraph processes the headers, eg. for <hgroup>
func (doc *Document) processHeaderParagraph(headerLineNum int) int {
	var tagName, htmlTag, restLine string
	var i int

	if debug {
		fmt.Println("********** Start HEADER", headerLineNum)
		defer fmt.Println("********** End HEADER", headerLineNum)
	}

	// The header should be just the first line
	thisIndentation := doc.Indentation(headerLineNum)
	nextIndentation := doc.Indentation(headerLineNum + 1)
	indentStr := strings.Repeat(" ", thisIndentation)

	// Process the paragraph with attributes
	tagName, htmlTag, restLine = doc.processTagSpec(headerLineNum)

	if !contains(headingElements, tagName) {
		doc.log.Fatalf("No header tag found in line %v\n", headerLineNum+1)
	}

	// If the next line is empty or indented less than the header, we are done with the header
	if len(doc.lines[headerLineNum+1]) == 0 || nextIndentation < thisIndentation {
		// Write the first line and the end tag
		doc.sb.WriteString(fmt.Sprintf("%v%v%v</%v>\n\n", indentStr, htmlTag, restLine, tagName))

		// Return the next line number to continue processing
		return headerLineNum + 1
	}

	// Create an hgroup with the header and the rest of contiguous lines in the paragraph
	doc.sb.WriteString(fmt.Sprintf("%v<hgroup>\n", indentStr))
	doc.sb.WriteString(fmt.Sprintf("%v  %v%v\n", indentStr, htmlTag, restLine))
	doc.sb.WriteString(fmt.Sprintf("%v  </%v>\n", indentStr, tagName))

	// Process the rest of contiguous lines in the block
	i = doc.processParagraph(headerLineNum + 1)

	doc.sb.WriteString(fmt.Sprintf("%v</%v>\n\n", indentStr, "hgroup"))

	// Return the next line to process
	return i

}

func (doc *Document) indentStr(lineNum int) string {
	return strings.Repeat(" ", doc.Indentation(lineNum))
}

func (doc *Document) ProcessList(startLineNum int) int {
	var i int

	// startLineNum should point to the <ul> or <ol> tag.
	// We expect the block to consist of a sequence of "li" elements, each of them can be as complex as needed
	// We first search for the first list element. It is an error if there is none

	doc.log.Debugw("ProcessList enter", "line", startLineNum+1)
	defer doc.log.Debugw("ProcessList exit", "line", startLineNum+1)

	tagFields := doc.preprocessTagSpec(startLineNum)

	// Sanity check: verify that only "ol" or "ul" are accepted
	if tagFields == nil {
		doc.log.Fatalw("no tag, expecting lists ol or ul", "line", startLineNum+1)
	}
	if tagFields["tag"] != "ol" && tagFields["tag"] != "ul" {
		doc.log.Fatalw("invalid tag, expecting lists ol or ul", "line", startLineNum+1)
	}

	// Calculate the unique list ID, if it was not specified by the user
	listID := tagFields["id"]
	if len(listID) == 0 {
		listID = strconv.Itoa(startLineNum + 1)
	}

	listTagName, listHtmlTag, listRestLine := doc.buildTagPresentation(startLineNum, tagFields)

	// List items must have indentation greater that the ol/ul tags
	listIndentation := doc.Indentation(startLineNum)

	// Write the first line, wrapping its text in a <p> if not empty
	doc.log.Debugw("ProcessList start-of-list tag", "line", startLineNum+1)
	if len(listRestLine) > 0 {
		doc.sb.WriteString(fmt.Sprintf("\n%v%v<p>%v</p>\n", doc.indentStr(startLineNum), listHtmlTag, listRestLine))
	} else {
		doc.sb.WriteString(fmt.Sprintf("\n%v%v\n", doc.indentStr(startLineNum), listHtmlTag))
	}

	itemIndentation := 0
	itemNumber := 0

	// Process each of the list items
	for i = startLineNum + 1; i < len(doc.lines); {

		// Do nothing if the line is empty
		if len(doc.lines[i]) == 0 {
			i++
			continue
		}

		line := doc.lines[i]

		// The indentation of the first list item sets the expected indentation for all other items
		if itemIndentation == 0 {
			itemIndentation = doc.Indentation(i)
		}

		// If the line has less or equal indentation than the ol/ul tags, stop processing this block
		if doc.Indentation(i) <= listIndentation {
			break
		}

		// We have a line that must be a list item
		var tagName, htmlTag, restLine, bulletText string

		if strings.HasPrefix(line, string(startTag)+"li") || strings.HasPrefix(line, string(startHTMLTag)+"li") {

			// This is a list item, increment the counter
			itemNumber++

			// Decompose the tag in its elements
			tagFields := doc.preprocessTagSpec(i)

			// The user may have specified a bullet text to start the list
			if len(tagFields["number"]) > 0 {
				itemID := strings.ReplaceAll(tagFields["number"], "%20", "_")
				listNumber := strings.ReplaceAll(tagFields["number"], "%20", " ")
				delete(tagFields, "number")
				tagFields["id"] = itemID
				bulletText = fmt.Sprintf("<a href='#%v' class='selfref'><b>%v.</b></a>", itemID, listNumber)
			} else {
				// Calculate the list item ID if it was not specified by the user
				itemID := tagFields["id"]
				if len(itemID) == 0 {
					itemID = listID + "." + strconv.Itoa(itemNumber)
					tagFields["id"] = itemID
				}
			}

			// Build the tag for presentation
			tagName, htmlTag, restLine = doc.buildTagPresentation(i, tagFields)

		} else {
			doc.log.Fatalf("line %v, this is not a list element: %v", i+1, line)
		}

		// Write the first line of the list item
		doc.log.Debugw("ProcessList item open tag", "line", i+1)
		doc.sb.WriteString(fmt.Sprintf("%v%v<p>%v%v</p>\n", strings.Repeat(" ", itemIndentation), htmlTag, bulletText, restLine))

		// Skip all the blank lines after the first line
		i = doc.skipBlankLines(i + 1)
		if doc.AtEOF(i) {
			doc.log.Infof("EOF reached at line %v\n", i+1)
			break
		}

		if doc.Indentation(i) > itemIndentation {
			// Process the following lines as a block
			doc.log.Debugw("ProcessList before ProcessBlock", "line", i+1)
			doc.sb.WriteString(fmt.Sprintf("%v<div>\n", strings.Repeat(" ", itemIndentation)))
			i = doc.ProcessBlock(i)
			doc.sb.WriteString(fmt.Sprintf("%v</div>\n", strings.Repeat(" ", itemIndentation)))
			doc.log.Debugw("ProcessList after ProcessBlock", "line", i+1)
		}

		// Write the list item end tag
		doc.log.Debugw("ProcessList item close tag", "line", i+1)
		doc.sb.WriteString(fmt.Sprintf("%v</%v>\n", strings.Repeat(" ", itemIndentation), tagName))

	}

	// Write the end-of-list tag
	doc.log.Debugw("ProcessList end-of-list tag", "line", startLineNum+1)
	doc.sb.WriteString(fmt.Sprintf("%v</%v>\n\n", strings.Repeat(" ", listIndentation), listTagName))

	return i

}

func (doc *Document) startsWithVerbatim(lineNum int) bool {
	line := doc.lines[lineNum]
	return strings.HasPrefix(line, "<pre")
}

func (doc *Document) startsWithList(lineNum int) bool {
	line := doc.lines[lineNum]
	return strings.HasPrefix(line, "<ol") || strings.HasPrefix(line, "<ul")
}

func (doc *Document) processVerbatim(startLineNum int) int {
	// This is a verbatim section, so we write it without processing
	tagName, htmlTag, restLine := doc.processTagSpec(startLineNum)

	thisIndentation := doc.Indentation(startLineNum)
	indentStr := strings.Repeat(" ", doc.Indentation(startLineNum))

	startOfNextBlock := 0
	lastNonEmptyLineNum := 0
	minimumIndentation := doc.indentations[startLineNum+1]

	for i := startLineNum + 1; !doc.AtEOF(i); i++ {

		verbatimIndentation := doc.Indentation(i)

		if len(doc.lines[i]) > 0 {

			if verbatimIndentation <= thisIndentation {
				startOfNextBlock = i
				break
			}

			lastNonEmptyLineNum = i
			if verbatimIndentation < minimumIndentation {
				minimumIndentation = verbatimIndentation
			}

		}

		if len(doc.lines[i]) > 0 && verbatimIndentation <= thisIndentation {
			startOfNextBlock = i
			break
		}

		if len(doc.lines[i]) > 0 {
			lastNonEmptyLineNum = i
		}

	}

	for i := startLineNum + 1; i <= lastNonEmptyLineNum; i++ {

		thisIndentationStr := ""
		if doc.Indentation(i)-minimumIndentation > 0 {
			thisIndentationStr = strings.Repeat(" ", doc.Indentation(i)-minimumIndentation)
		}

		if i == startLineNum+1 {
			// Write the first line
			doc.sb.WriteString(fmt.Sprintf("\n%v%v%v%v\n", indentStr, htmlTag, restLine, doc.lines[i]))

		} else if i == lastNonEmptyLineNum {
			// Write the end tag
			// As a very common special case, if there was a <code> in the same line as <pre>, write the end tag too
			if strings.HasPrefix(restLine, "<code") {
				doc.sb.WriteString(fmt.Sprintf("%v%v</code></%v>\n\n", thisIndentationStr, doc.lines[i], tagName))
			} else {
				doc.sb.WriteString(fmt.Sprintf("%v%v</%v>\n\n", thisIndentationStr, doc.lines[i], tagName))
			}

		} else {
			// Write the verbatim line
			doc.sb.WriteString(fmt.Sprintf("%v%v\n", thisIndentationStr, doc.lines[i]))

		}

	}

	return startOfNextBlock

}

func (doc *Document) ProcessSectionTag(startLineNum int) int {
	// Section starts with a tag spec. Process the tag and
	// advance the line pointer appropriately
	var restLine string
	tagName, htmlTag, restLine := doc.processTagSpec(startLineNum)
	thisIndentation := doc.indentations[startLineNum]

	// Write the first line, wrapping its text in a <p> if not empty and if the tag is not a <p> itself
	// We add a blank line before, to make the output more readable
	// if len(restLine) > 0 && tagName != "p" {
	// 	restLine = "<p>" + restLine + "</p>"
	// }
	doc.sb.WriteString(fmt.Sprintf("\n%v%v%v\n", doc.indentStr(startLineNum), htmlTag, restLine))

	// If the next non-blank line is indented the same, we write the end tag and return
	// Otherwise, we start and process a new indented block

	// Skip all the blank lines
	nextLineNum := doc.skipBlankLines(startLineNum + 1)
	if doc.AtEOF(nextLineNum) {
		doc.log.Debugf("EOF reached at line %v", startLineNum+1)
		return nextLineNum
	}

	// Start and process an indented block if the next line is more indented
	nextIndentation := doc.Indentation(nextLineNum)
	if nextIndentation > thisIndentation {
		nextLineNum = doc.ProcessBlock(nextLineNum)
	}

	// Write the end tag for the section
	if isVoidElement(tagName) {
		// HTML spec says no end tag should be used
		doc.sb.WriteString(fmt.Sprintln())
	} else {
		doc.sb.WriteString(fmt.Sprintf("%v</%v>\n\n", doc.indentStr(startLineNum), tagName))

	}

	// Return the next line to process
	return nextLineNum

}

// ProcessBlock recursively processes a document taking into account indentation.
// A document is a block and a block is composed of either:
//   - Paragraphs separated by blank lines
//   - Indented blocks, called sections
func (doc *Document) ProcessBlock(startLineNum int) int {
	var currentLineNum int

	// Skip all the blank lines at the beginning of the block
	startLineNum = doc.skipBlankLines(startLineNum)
	if doc.AtEOF(startLineNum) {
		doc.log.Infof("EOF reached at line %v\n", startLineNum)
		return startLineNum
	}

	// Calculate indentation of the first line to process
	// This is going to be the indentation of the current block to process
	thisBlockIndentation := doc.Indentation(startLineNum)

	// In this loop we process all paragraphs at the same indentation or higher
	// We stop processing the block when the indentation decreases or we reach the EOF
	for currentLineNum = startLineNum; !doc.AtEOF(currentLineNum); {

		currentLine := doc.lines[currentLineNum]
		currentLineIndentation := doc.Indentation(currentLineNum)

		// If the line is empty, just go to the next one
		if len(currentLine) == 0 {
			currentLineNum++
			continue
		}
		prefixLen := len(currentLine)
		if prefixLen > 4 {
			prefixLen = 4
		}

		doc.log.Debugw("ProcessBlock", "line", currentLineNum, "indent", currentLineIndentation, "l", currentLine[:prefixLen])

		// If the line has less indentation than the block, stop processing this block
		if currentLineIndentation < thisBlockIndentation {
			break
		}

		// If indentation is greater, we start a new Block
		if currentLineIndentation > thisBlockIndentation {
			currentLineNum = doc.ProcessBlock(currentLineNum)
			continue
		}

		// A verbatim section that is not processed
		if doc.startsWithVerbatim(currentLineNum) {
			currentLineNum = doc.processVerbatim(currentLineNum)
			continue
		}

		// Headers have some special processing
		if doc.startsWithHeaderTag(currentLineNum) {
			currentLineNum = doc.processHeaderParagraph(currentLineNum)
			continue
		}

		// Lists have also some special processing
		if doc.startsWithList(currentLineNum) {
			currentLineNum = doc.ProcessList(currentLineNum)
			continue
		}

		// Any other tag which starts a section, like div, p, section, article, ...
		if doc.startsWithSectionTag(currentLineNum) {
			currentLineNum = doc.ProcessSectionTag(currentLineNum)
			continue
		}

		// A line without any section tag starts a paragraph block
		currentLineNum = doc.processParagraph(currentLineNum)

	}

	return currentLineNum

}

func processWatch(inputFileName string, outputFileName string, sugar *zap.SugaredLogger) error {

	var old_timestamp time.Time
	var current_timestamp time.Time

	for {
		info, err := os.Stat(inputFileName)
		if err != nil {
			return err
		}

		current_timestamp = info.ModTime()

		if old_timestamp.Before(info.ModTime()) {
			old_timestamp = current_timestamp
			fmt.Println("************Processing*************")
			b := NewDocumentFromFile(inputFileName, sugar)
			html := b.ToHTML()
			err = os.WriteFile(outputFileName, []byte(html), 0664)
			if err != nil {
				return err
			}
		}

		time.Sleep(1 * time.Second)

	}
}

func process(c *cli.Context) error {

	// Default input file name
	var inputFileName = "index.txt"

	// Output file name command line parameter
	outputFileName := c.String("output")

	// Dry run
	dryrun := c.Bool("dryrun")

	debug = c.Bool("debug")

	var z *zap.Logger
	var err error

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

	sugar := z.Sugar()
	defer sugar.Sync()

	// Get the input file name
	if c.Args().Present() {
		inputFileName = c.Args().First()
	} else {
		fmt.Printf("no input file provided, using \"%v\"\n", inputFileName)
	}

	// Generate the output file name
	if len(outputFileName) == 0 {
		ext := path.Ext(inputFileName)
		if len(ext) == 0 {
			outputFileName = inputFileName + ".html"
		} else {
			outputFileName = strings.Replace(inputFileName, ext, ".html", 1)
		}
	}

	// Print a message
	if !dryrun {
		fmt.Printf("processing %v and generating %v\n", inputFileName, outputFileName)
	} else {
		fmt.Printf("dry run: processing %v without writing output\n", inputFileName)
	}

	if c.Bool("watch") {
		processWatch(inputFileName, outputFileName, sugar)
		return nil
	}

	b := NewDocumentFromFile(inputFileName, sugar)

	if debug {
		b.printPreprocessStats()
	}

	html := b.ToHTML()

	if dryrun {
		return nil
	}

	err = os.WriteFile(outputFileName, []byte(html), 0664)
	if err != nil {
		return err
	}

	return nil
}

func main() {

	app := &cli.App{
		Name:     "rite",
		Version:  "v1.01",
		Compiled: time.Now(),
		Authors: []*cli.Author{
			{
				Name:  "Jesus Ruiz",
				Email: "hesus.ruiz@gmail.com",
			},
		},
		Usage:     "process a rite document and produce HTML",
		UsageText: "rite [options] [INPUT_FILE] (default input file is index.txt)",
		Action:    process,
		ArgsUsage: "perico perez",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "write html to `FILE` (default is input file name with extension .html)",
			},
			&cli.BoolFlag{
				Name:    "dryrun",
				Aliases: []string{"n"},
				Usage:   "do not generate output file, just process input file",
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "run in debug mode",
			},
			&cli.BoolFlag{
				Name:    "watch",
				Aliases: []string{"w"},
				Usage:   "watch the file for changes",
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		panic(err)
	}

}
