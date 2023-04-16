// Copyright 2023 Jesus Ruiz. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/hesusruiz/vcutils/yaml"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"

	"github.com/hesusruiz/rite/sliceedit"

	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2themes/d2themescatalog"
	"oss.terrastruct.com/d2/lib/textmeasure"
)

var log *zap.SugaredLogger

var norespec bool
var debug bool

const startHTMLTag = '<'
const endHTMLTag = '>'

var voidElements = []string{
	"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr",
}
var noSectionElements = []string{
	"b", "i", "hr", "em", "strong", "small", "s",
}
var headingElements = []string{"h1", "h2", "h3", "h4", "h5", "h6"}

// Heading maintains the hierarchical structure of the headins to be able to number them automatically
type Heading struct {
	subheadings []*Heading
}

type Outline struct {
	doc         *Document
	subheadings []*Heading
}

func (o *Outline) addHeading(tag *TagStruct) ([]byte, error) {

	tagName := string(tag.Tag)

	// The first time should be an h1 tag
	if len(o.subheadings) == 0 && tagName != "h1" {
		return nil, fmt.Errorf("addHeading, line %v: expected h1 , received %s", o.doc.lastLine, tagName)
	}
	var index int
	for index = 0; index < len(headingElements); index++ {
		if headingElements[index] == tagName {
			break
		}
	}
	if index == len(headingElements) {
		return nil, fmt.Errorf("addHeading, line %v: invalid tag %s, expecting a heading", o.doc.lastLine, tagName)
	}

	_, htmlTag, rest := tag.Render()

	newHeading := &Heading{}
	switch tagName {
	case "h1":
		o.subheadings = append(o.subheadings, newHeading)
		r := fmt.Appendf([]byte{}, "%s<span class='secno'>%d</span> %s", htmlTag, len(o.subheadings), rest)
		return r, nil
	case "h2":
		if len(o.subheadings) == 0 {
			return nil, fmt.Errorf("addHeading, line %v: adding '%v' but no 'h1' exists", o.doc.lastLine, tagName)
		}
		l1 := o.subheadings[len(o.subheadings)-1]
		l1.subheadings = append(l1.subheadings, newHeading)

		r := fmt.Appendf([]byte{}, "%s<span class='secno'>%d.%d</span> %s", htmlTag, len(o.subheadings), len(l1.subheadings), rest)
		return r, nil
	case "h3":
		if len(o.subheadings) == 0 {
			return nil, fmt.Errorf("addHeading, line %v: adding '%v' but no 'h1' exists", o.doc.lastLine, tagName)
		}
		l1 := o.subheadings[len(o.subheadings)-1]
		if len(l1.subheadings) == 0 {
			return nil, fmt.Errorf("addHeading, line %v: adding '%v' but no 'h2' exists", o.doc.lastLine, tagName)
		}
		l2 := l1.subheadings[len(l1.subheadings)-1]
		l2.subheadings = append(l2.subheadings, newHeading)
		r := fmt.Appendf([]byte{}, "%s<span class='secno'>%d.%d.%d</span> %s", htmlTag, len(o.subheadings), len(l1.subheadings), len(l1.subheadings), rest)
		return r, nil
	}
	return nil, nil
}

type tagType int

const (
	PARAGRAPH tagType = 0
	HEADER    tagType = 1
	LIST      tagType = 2
	D2DIAGRAM tagType = 3
	PRE       tagType = 4
)

type TagStruct struct {
	Typ         tagType
	OriginalTag []byte
	Tag         []byte
	Id          []byte
	Class       []byte
	Src         []byte
	Href        []byte
	Bucket      []byte
	Number      []byte
	StdFields   []byte
	RestLine    []byte
}

type LineStruct struct {
	startTag    *TagStruct
	indentation int
	line        []byte
}

type Document struct {
	renderer strings.Builder // Used to render efficiently the HTML document in the final phase
	theLines []*LineStruct   // All the lines of the document
	lastLine int             // Used for logging and error reporting
	ids      map[string]int  // To provide numbering of different entity classes
	figs     map[string]int  // To provide numbering of figs of different types in the document
	config   *yaml.YAML
}

func (doc *Document) Size() int {
	return len(doc.theLines)
}

func (doc *Document) ValidLineNum(lineNum int) bool {
	if lineNum < 0 || lineNum >= len(doc.theLines) {
		return false
	}
	return true
}

func (doc *Document) Line(lineNum int) []byte {
	if !doc.ValidLineNum(lineNum) {
		log.Fatalw("invalid line number", "line", lineNum)
	}
	return doc.theLines[lineNum].line
}

func (doc *Document) UpdateLine(lineNum int, newLine []byte) {
	if !doc.ValidLineNum(lineNum) {
		log.Fatalw("invalid line number", "line", lineNum)
	}

	// Update the line
	doc.theLines[lineNum].line = newLine

	// Update the tag spec
	tagFields, err := doc.preprocessTagSpec(newLine)
	if err != nil {
		log.Fatalf("UpdateLine, line %d: %s", doc.lastLine, err)
	}
	doc.theLines[lineNum].startTag = tagFields
}

func (doc *Document) StartTagForLine(lineNum int) *TagStruct {
	if !doc.ValidLineNum(lineNum) {
		log.Fatalw("invalid line number", "line", lineNum)
	}
	return doc.theLines[lineNum].startTag
}

func (t *TagStruct) Render() (tagName []byte, htmlTag []byte, rest []byte) {
	// Sanity check
	if t == nil {
		return nil, nil, nil
	}

	htmlTag = fmt.Appendf(htmlTag, "<%s", t.Tag)
	if len(t.Id) > 0 {
		htmlTag = fmt.Appendf(htmlTag, " id='%s'", t.Id)
	}
	if len(t.Class) > 0 {
		htmlTag = fmt.Appendf(htmlTag, " class='%s'", t.Class)
	}
	if len(t.Href) > 0 {
		htmlTag = fmt.Appendf(htmlTag, " href='%s'", t.Href)
	}
	if len(t.StdFields) > 0 {
		htmlTag = fmt.Appendf(htmlTag, " %s", t.StdFields)
	}
	htmlTag = fmt.Appendf(htmlTag, ">")

	return t.Tag, htmlTag, t.RestLine

}

func (doc *Document) RenderTagForLine(lineNum int) (tagName []byte, htmlTag []byte, rest []byte) {
	if !doc.ValidLineNum(lineNum) {
		log.Fatalw("invalid line number", "line", lineNum)
	}

	tagFields := doc.StartTagForLine(lineNum)

	return tagFields.Render()

}

func (doc *Document) StartTagType(lineNum int) tagType {
	if !doc.ValidLineNum(lineNum) {
		return -1
	}
	if doc.theLines[lineNum].startTag == nil {
		return PARAGRAPH
	}
	return doc.theLines[lineNum].startTag.Typ
}

func trimLeft(input []byte, c byte) []byte {
	for len(input) > 0 && input[0] == c {
		input = input[1:]
	}
	if len(input) == 0 {
		return nil
	}
	return input
}

// *******************************************
// *******************************************
// *******************************************

var prefixPre = []byte("<pre")

func lineStartsWithPre(line []byte) bool {
	return bytes.HasPrefix(line, prefixPre)
}

var prefixD2 = []byte("<x-diagram .d2>")

func lineStartsWithD2(line []byte) bool {
	return bytes.HasPrefix(line, prefixD2)
}

// lineStartsWithTag returns true if the line starts with a start tags character
func lineStartsWithTag(line []byte) bool {
	// Check standard HTML tag
	return len(line) > 0 && line[0] == startHTMLTag
}

// lineStartsWithHeaderTag returns true if the line starts with <h1>, <h2>, ...
func (doc *Document) lineStartsWithHeaderTag(lineNum int) bool {

	line := doc.Line(lineNum)

	if len(line) < 4 || line[0] != startHTMLTag {
		return false
	}
	return contains(headingElements, line[1:3])
}

// lineStartsWithSectionTag returns true if the line:
//
//	starts either with the HTML tag ('<') or our special tag
//	and it is followed by a blank line or a line which is more indented
func (doc *Document) lineStartsWithSectionTag(lineNum int) bool {

	// Decompose the tag into its elements
	tagFields, _ := doc.preprocessTagSpec(doc.Line(lineNum))

	// Return false if there is no tag or it is in the sets that we know should not start a section
	// For example, void elements
	if tagFields == nil || isNoSectionElement(tagFields.Tag) || contains(voidElements, tagFields.Tag) {
		return false
	}

	return true

}

func (doc *Document) lineStartsWithList(lineNum int) bool {
	line := doc.Line(lineNum)
	return bytes.HasPrefix(line, []byte("<ol")) || bytes.HasPrefix(line, []byte("<ul"))
}

// *******************************************
// *******************************************
// *******************************************
// *******************************************

// NewDocument parses the input one line at a time, preprocessing the lines and building
// a parsed document ready to be processed
func NewDocument(s *bufio.Scanner) (*Document, error) {

	// This regex detects the <x-ref REFERENCE> tags that need special processing
	reXRef := regexp.MustCompile(`<x-ref +([0-9a-zA-Z-_\.]+) *>`)
	reCodeBackticks := regexp.MustCompile(`\x60([0-9a-zA-Z-_\.]+)\x60`)

	// Verbatim sections require special processing to keep their exact format
	insideVerbatim := false
	indentationVerbatim := 0

	// Create and initialize the document structure
	doc := &Document{}
	doc.ids = make(map[string]int)
	doc.figs = make(map[string]int)

	// Initialize the structure to keep the tree of headers in the document
	outline := &Outline{}
	outline.doc = doc

	// We build in memory the parsed document, pre-processing all lines as we read them.
	// This means that in this stage we can not use information that resides later in the file.
	for s.Scan() {

		// Get a rawLine from the file
		rawLine := bytes.Clone(s.Bytes())

		// Strip blanks at the beginning and calculate indentation based on the difference in length
		// We do not support other whitespace like tabs
		line := trimLeft(rawLine, ' ')
		indentation := len(rawLine) - len(line)

		// Calculate the line number
		lineNum := doc.Size()
		doc.lastLine = lineNum + 1

		// Add the line struct
		theLine := &LineStruct{}
		theLine.indentation = indentation
		theLine.line = line
		doc.theLines = append(doc.theLines, theLine)

		// Do not process the line if it is empty
		if len(line) == 0 {
			continue
		}

		// Special processing for verbatim areas, started by a <pre> or diagram D2 tag.
		// Everyting inside a verbatim area should be left exactly as it is.
		if insideVerbatim {
			// Do not process the line if we are inside a verbatim area
			if indentation > indentationVerbatim {
				continue
			}
			// Check if we exited the verbatim area
			if indentation <= indentationVerbatim {
				insideVerbatim = false
			}
		} else if lineStartsWithPre(line) || lineStartsWithD2(line) {
			// The verbatim area is indicated by a <pre> or diagram D2 tag
			insideVerbatim = true

			// Remember the indentation of the tag
			// Verbatim content has to be indented (indentation > indentationVerbatim)
			indentationVerbatim = indentation
		}

		// We ignore any line starting with an end tag
		if bytes.HasPrefix(line, []byte("</")) {
			doc.theLines[lineNum] = nil
			continue
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
			// if debug {
			// 	fmt.Println("preprocessed Header ##:", string(doc.lines[lineNum]))
			// }

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
				return nil, fmt.Errorf("NewDocument, line %v: no closing ')' in list bullet", lineNum+1)
			}

			// Check that there is at least one character inside the '()'
			if indexRightBracket == 2 {
				return nil, fmt.Errorf("NewDocument, line %v: no content inside '()' in list bullet", lineNum+1)
			}

			// Extract the whole tag spec, eliminating embedded blanks
			bulletText := line[2:indexRightBracket]
			bulletText = bytes.ReplaceAll(bulletText, []byte(" "), []byte("%20"))

			// And the remaining text in the line
			restLine := line[indexRightBracket+1:]

			// Update the line
			line = append([]byte("<li ="), bulletText...)
			line = append(line, '>')
			line = append(line, restLine...)

		}

		// Update the line in the struct
		doc.theLines[lineNum].line = line

		// Parse the tag at the beginning of the line
		tagFields, _ := doc.preprocessTagSpec(line)
		if tagFields == nil {
			// No tag found, just continue with the next line
			continue
		}

		// Preprocess tags with ID fields so they can be referenced later
		// We also keep a counter so they can be numbered in the final HTML
		id := tagFields.Id
		if len(id) > 0 {

			// If the user specified the "type" attribute, we use its value as a classification bucket for numbering
			bucket := tagFields.Bucket
			if len(bucket) == 0 {
				// Otherwise, we use the name of the tag as a classification bucket
				bucket = tagFields.Tag
			}

			// As an example, if the user does not specify anything, all <figures> with an id will be in the
			// same bucket and the counter will be incremented for each figure. But the user may differentiate
			// figures with images from figures with tables (for example). She can use the special attribute
			// like this: '<figure #picture1 :photos>' or for tables '<figure #tablewithgrowthrate :tables> The
			// names of the buckets (the string after the ':') can be any, and there may be as many as needed.

			// We don't allow duplicate id
			if doc.ids[string(id)] > 0 {
				return nil, fmt.Errorf("NewDocuemnt, line %d: id already used %s", lineNum+1, id)
			}

			// Increment the number of elements in this bucket
			doc.figs[string(bucket)] = doc.figs[string(bucket)] + 1
			// And set the current value of the counter for this id.
			doc.ids[string(id)] = doc.figs[string(bucket)]

		}

		// Update the line in the structure
		doc.theLines[lineNum].line = line

		// Add the tag info to the line
		doc.theLines[lineNum].startTag = tagFields

		// Preprocess headings (h1, h2, h3, ...), creating the tree of content to display hierarchical numbering.
		// To enforce the HTML5 spece, we accept a heading of a given level only if it is the same level,
		// one more or one less than the previously encountered heading. H1 are always accepted in any context.
		// We do this only if not using ReSpec format, in which case numbering will be done by ReSpec
		// tagName, htmlTag, rest := doc.buildTagPresentation(tagFields)
		if norespec && doc.lineStartsWithHeaderTag(lineNum) {

			// If header is marked as "no-num" we do not include it in the header numbering schema
			if !bytes.Contains(tagFields.OriginalTag, []byte("no-num")) {

				newLine, err := outline.addHeading(tagFields)
				if err != nil {
					return nil, err
				}
				if len(newLine) > 0 {
					doc.UpdateLine(lineNum, newLine)
				}

			}

		}

	}

	// Check if there was any error
	err := s.Err()
	if err != nil {
		log.Errorw("error scanning the input file", "err", err)
	}

	return doc, nil

}

// preprocessTagSpec returns a map with the tag fields, or nil if not a tag
func (doc *Document) preprocessTagSpec(rawLine []byte) (*TagStruct, error) {
	var tagSpec, restLine []byte

	// Sanity check
	if rawLine[0] != startHTMLTag {
		return nil, fmt.Errorf("preprocessTagSpec, line %d: line does not start with a tag", doc.lastLine)
	}

	t := &TagStruct{}

	// Trim the start and end tag chars
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

	// Process the special shortcut syntax we provide
	for i := 1; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 2 {
			return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", doc.lastLine)
		}

		switch f[0] {
		case '#':
			// Shortcut for id="xxxx"
			t.Id = f[1:]
		case '.':
			// Shortcut for class="xxxx"
			t.Class = f[1:]
		case '@':
			// Shortcut for src="xxxx"
			t.Src = f[1:]
		case '-':
			// Shortcut for href="xxxx"
			t.Href = f[1:]
		case ':':
			// Special attribute "type" for item classification and counters
			t.Bucket = f[1:]
		case '=':
			// Special attribute "number" for list items
			t.Number = f[1:]
		default:
			// Special cases should be at the beginning. If we are here, the rest is normal HTML attributes
			t.StdFields = bytes.Join(fields[i:], []byte(" "))
		}
	}

	// The rest of the input line after the tag is available in the "restLine" element
	t.RestLine = restLine

	return t, nil
}

func (doc *Document) printPreprocessStats() {
	fmt.Printf("Number of lines: %v\n", doc.Size())
	fmt.Println()
	fmt.Printf("Number of ids: %v\n", len(doc.ids))
	for k, v := range doc.figs {
		fmt.Printf("  %v: %v\n", k, v)
	}
}

// ***************************************************************
// ***************************************************************
// ***************************************************************

func (doc *Document) preprocessYAMLHeader() int {
	var err error

	// We accept YAML data only at the beginning of the file
	if !bytes.HasPrefix(doc.Line(0), []byte("---")) {
		log.Debugln("no YAML metadata found")
		return 0
	}

	// Build a string with all lines up to the next "---"
	var currentLineNum int
	var yamlString strings.Builder
	for currentLineNum = 1; currentLineNum < doc.Size(); currentLineNum++ {
		if bytes.HasPrefix(doc.Line(currentLineNum), []byte("---")) {
			currentLineNum++
			break
		}

		yamlString.Write(doc.Line(currentLineNum))
		yamlString.WriteString("\n")

	}

	// Parse the string that was built as YAML data
	doc.config, err = yaml.ParseYaml(yamlString.String())
	if err != nil {
		log.Fatalw("malformed YAML metadata", "error", err)
	}

	// Return the line number after the YAML header
	return currentLineNum
}

func (doc *Document) SetLogger(logger *zap.SugaredLogger) {
	log = logger
}

func contains(set []string, tagName []byte) bool {
	for _, el := range set {
		if string(tagName) == el {
			return true
		}
	}
	return false
}

// isVoidElement returns true if the tag is in the set of 'void' tags
func isVoidElement(tagName []byte) bool {
	for _, el := range voidElements {
		if string(tagName) == el {
			return true
		}
	}
	return false
}

// isNoSectionElement returns true if the tag is in the set of 'noSectionElements' tags
func isNoSectionElement(tagName []byte) bool {
	for _, el := range noSectionElements {
		if string(tagName) == el {
			return true
		}
	}
	return false
}

const EOF = -1

// AtEOF returns true if the line number is beyond the end of file
func (doc *Document) AtEOF(lineNum int) bool {
	return lineNum == EOF || lineNum >= doc.Size()
}

func (doc *Document) Indentation(lineNum int) int {
	return doc.theLines[lineNum].indentation
}

// skipBlankLines returns the line number of the first non-blank line,
// starting from the provided line number, or EOF if there are no more blank lines.
// If the start line is non-blank, we return that line.
func (doc *Document) skipBlankLines(lineNumber int) int {
	var trimmedLine []byte

	for i := lineNumber; i < doc.Size(); i++ {

		// Trim all blanks to see if the line is a blank line
		trimmedLine = bytes.TrimSpace(doc.Line(i))

		// Return if non-blank
		if len(trimmedLine) > 0 {
			return i
		}

	}

	// Return the size of the file (one more than the last line number)
	// This is used as an indication that we are at End of File
	return doc.Size()
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
		log.Fatalw("error reading template", "error", err, "name", templateName)
		panic(err)
	}
	rawHtml := bytes.Replace(tmpl, []byte("HERE_GOES_THE_CONTENT"), []byte(doc.renderer.String()), 1)

	edBuf := sliceedit.NewBuffer(rawHtml)

	// The title in the metadata
	title := doc.config.String("title", "title")

	searchString := "{#title}"
	edBuf.ReplaceAllString(searchString, title)

	// For all IDs that were detected
	for idName, idNumber := range doc.ids {
		searchString := "{#" + idName + ".num}"
		newValue := fmt.Sprint(idNumber)
		edBuf.ReplaceAllString(searchString, newValue)
	}

	html := edBuf.String()

	return html
}

// processParagraph reads all contiguous lines of a block, unless it encounters some special tag at the beginning
func (doc *Document) processParagraph(startLineNum int) int {
	var tagName, htmlTag, startLine []byte
	var i int
	var nextLineNum int

	// We process all contiguous lines without taking into account its indentation
	rawLine := doc.Line(startLineNum)

	if lineStartsWithTag(rawLine) {

		// Process the paragraph with attributes
		tagName, htmlTag, startLine = doc.RenderTagForLine(startLineNum)

		if isNoSectionElement(tagName) {
			// A normal paragraph without any command
			startLine = rawLine
			nextLineNum = startLineNum + 1
			tagName = []byte("p")

			// Write the first line
			doc.renderer.WriteString(fmt.Sprintf("%v<%v>%v\n", strings.Repeat(" ", doc.Indentation(startLineNum)), string(tagName), string(startLine)))

		} else {
			// Write the first line
			doc.renderer.WriteString(fmt.Sprintf("%v%v%v\n", strings.Repeat(" ", doc.Indentation(startLineNum)), string(htmlTag), string(startLine)))

			// Point to the next line in the block (if there are any)
			nextLineNum = startLineNum + 1

		}

	} else {

		// A raw text which starts without any tag
		startLine = rawLine
		nextLineNum = startLineNum + 1
		tagName = []byte("p")

		// Write the first line
		doc.renderer.WriteString(fmt.Sprintf("%v<%v>%v\n", strings.Repeat(" ", doc.Indentation(startLineNum)), string(tagName), string(startLine)))
	}

	// Process the rest of contiguous lines in the block, writing them without any processing
	for i = nextLineNum; i < doc.Size(); i++ {
		line := doc.Line(i)
		if len(line) > 0 {
			doc.renderer.WriteString(fmt.Sprintf("%v%v\n", strings.Repeat(" ", doc.Indentation(i)), string(line)))
		} else {
			break
		}
	}

	// Write the end tag
	if isVoidElement(tagName) {
		// HTML spec says no end tag should be used
		doc.renderer.WriteString(fmt.Sprintln())
	} else {
		doc.renderer.WriteString(fmt.Sprintf("%v</%v>\n", strings.Repeat(" ", doc.Indentation(startLineNum)), string(tagName)))
	}

	// Return the next line to process
	return i

}

// processHeaderParagraph processes the headers, eg. for <hgroup>
func (doc *Document) processHeaderParagraph(headerLineNum int) int {
	var tagName, htmlTag, restLine []byte
	var i int

	if debug {
		fmt.Println("********** Start HEADER", headerLineNum+1)
		defer fmt.Println("********** End HEADER", headerLineNum+1)
	}

	// The header should be just the first line
	thisIndentation := doc.Indentation(headerLineNum)
	nextIndentation := doc.Indentation(headerLineNum + 1)
	indentStr := strings.Repeat(" ", thisIndentation)

	// Process the paragraph with attributes
	tagName, htmlTag, restLine = doc.RenderTagForLine(headerLineNum)

	if !contains(headingElements, tagName) {
		log.Fatalf("No header tag found in line %v\n", headerLineNum+1)
	}

	// If the next line is empty or indented less than the header, we are done with the header
	if len(doc.Line(headerLineNum+1)) == 0 || nextIndentation < thisIndentation {
		// Write the first line and the end tag
		doc.renderer.WriteString(fmt.Sprintf("%v%v%v</%v>\n\n", indentStr, string(htmlTag), string(restLine), string(tagName)))

		// Return the next line number to continue processing
		return headerLineNum + 1
	}

	// Here we have a header line and the next lines specifies a subheader
	// Create an hgroup with the header and the rest of contiguous lines in the paragraph
	doc.renderer.WriteString(fmt.Sprintf("%v<hgroup>\n", indentStr))
	doc.renderer.WriteString(fmt.Sprintf("%v  %v%v\n", indentStr, string(htmlTag), string(restLine)))
	doc.renderer.WriteString(fmt.Sprintf("%v  </%v>\n", indentStr, string(tagName)))

	// Process the rest of contiguous lines in the block
	i = doc.processParagraph(headerLineNum + 1)

	doc.renderer.WriteString(fmt.Sprintf("%v</%v>\n\n", indentStr, "hgroup"))

	// Return the next line to process
	return i

}

func (doc *Document) indentStr(lineNum int) string {
	return strings.Repeat(" ", doc.Indentation(lineNum))
}

func (doc *Document) ProcessList(startLineNum int) int {
	var currentLineNum int

	// startLineNum should point to the <ul> or <ol> tag.
	// We expect the block to consist of a sequence of "li" elements, each of them can be as complex as needed
	// We first search for the first list element. It is an error if there is none

	log.Debugw("ProcessList enter", "line", startLineNum+1)
	defer log.Debugw("ProcessList exit", "line", startLineNum+1)

	tagFields := doc.StartTagForLine(startLineNum)

	// Sanity check: verify that only "ol" or "ul" are accepted
	if tagFields == nil {
		log.Fatalw("no tag, expecting lists ol or ul", "line", startLineNum+1)
	}
	if string(tagFields.Tag) != "ol" && string(tagFields.Tag) != "ul" {
		log.Fatalw("invalid tag, expecting lists ol or ul", "line", startLineNum+1)
	}

	// Calculate the unique list ID, if it was not specified by the user
	listID := tagFields.Id
	if len(listID) == 0 {
		listID = fmt.Appendf(listID, "list_%v", startLineNum+1)
	}

	// Prepare for rendering the <li> line
	listTagName, listHtmlTag, listRestLine := tagFields.Render()

	// List items must have indentation greater than the ol/ul tags
	listIndentation := doc.Indentation(startLineNum)

	// Write the first line, wrapping its text in a <p> if not empty
	// We also add a newline at the beginning for better readability of the generated HTML (this has
	// no influence on the displayed page).
	log.Debugw("ProcessList start-of-list tag", "line", startLineNum+1)
	if len(listRestLine) > 0 {
		doc.renderer.WriteString(fmt.Sprintf("\n%v%v<p>%v</p>\n", doc.indentStr(startLineNum), string(listHtmlTag), string(listRestLine)))
	} else {
		doc.renderer.WriteString(fmt.Sprintf("\n%v%v\n", doc.indentStr(startLineNum), string(listHtmlTag)))
	}

	listContentIndentation := 0
	listItemNumber := 0

	// Process each of the list items until end of list or end of file
	for currentLineNum = startLineNum + 1; currentLineNum < doc.Size(); {

		// Do nothing if the line is empty
		if len(doc.Line(currentLineNum)) == 0 {
			currentLineNum++
			continue
		}

		// We have found the first item of the list
		line := doc.Line(currentLineNum)

		// Remember the indentation of the first line.
		// Its indentation sets the expected indentation for all other items.
		if listContentIndentation == 0 {
			// This is done only once for the whole list
			listContentIndentation = doc.Indentation(currentLineNum)
		}

		// If the line has less or equal indentation than the ol/ul tags, stop processing this block
		if doc.Indentation(currentLineNum) <= listIndentation {
			break
		}

		// We have a line that must be a list item
		var bulletText string
		var tagName, htmlTag, restLine []byte

		// Check if line starts with '<li'
		if bytes.HasPrefix(line, []byte("<li")) {

			// This is a list item, increment the counter
			listItemNumber++

			// Decompose the tag in its elements
			tagFields := doc.StartTagForLine(currentLineNum)

			// The user may have specified a bullet text to start the list
			if len(tagFields.Number) > 0 {
				itemID := fmt.Appendf(listID, ".%s", bytes.ReplaceAll(tagFields.Number, []byte("%20"), []byte("_")))
				listNumber := bytes.ReplaceAll(tagFields.Number, []byte("%20"), []byte(" "))
				tagFields.Id = itemID
				bulletText = fmt.Sprintf("<a href='#%v' class='selfref'><b>%v.</b></a>", string(itemID), string(listNumber))
			} else {
				// Calculate the list item ID if it was not specified by the user
				itemID := tagFields.Id
				if len(itemID) == 0 {
					itemID = fmt.Appendf(listID, ".%d", listItemNumber)
					tagFields.Id = itemID
				}
			}

			// Build the tag for presentation
			tagName, htmlTag, restLine = tagFields.Render()

		} else {
			log.Fatalf("line %v, this is not a list element: %v", currentLineNum+1, string(line))
		}

		// Write the first line of the list item
		log.Debugw("ProcessList item open tag", "line", currentLineNum+1)
		doc.renderer.WriteString(fmt.Sprintf("%v%v%v%v\n", strings.Repeat(" ", listContentIndentation), string(htmlTag), bulletText, string(restLine)))

		// Skip all the blank lines after the first line
		currentLineNum = doc.skipBlankLines(currentLineNum + 1)

		// We are finished if we have reached the end of the document
		if doc.AtEOF(currentLineNum) {
			log.Debugf("EOF reached at line %v\n", currentLineNum+1)
			break
		}

		// Each list item can have additional content which should be more indented
		// We wrap that content in a <div></div> section
		if doc.Indentation(currentLineNum) > listContentIndentation {
			log.Debugw("ProcessList before ProcessBlock", "line", currentLineNum+1)

			// Process the following lines as a block, inside a <div> section
			doc.renderer.WriteString(fmt.Sprintf("%v<div>\n", strings.Repeat(" ", listContentIndentation)))
			currentLineNum = doc.ProcessBlock(currentLineNum)
			doc.renderer.WriteString(fmt.Sprintf("%v</div>\n", strings.Repeat(" ", listContentIndentation)))

			log.Debugw("ProcessList after ProcessBlock", "line", currentLineNum+1)
		}

		// Write the list item end tag
		log.Debugw("ProcessList item close tag", "line", currentLineNum+1)
		doc.renderer.WriteString(fmt.Sprintf("%v</%v>\n", strings.Repeat(" ", listContentIndentation), string(tagName)))

	}

	// Write the end-of-list tag
	log.Debugw("ProcessList end-of-list tag", "line", startLineNum+1)
	doc.renderer.WriteString(fmt.Sprintf("%v</%v>\n\n", strings.Repeat(" ", listIndentation), string(listTagName)))

	// Return the line number following the already processed list
	return currentLineNum

}

func (doc *Document) processVerbatim(startLineNum int) int {
	log.Debugw("ProcessVerbatim", "line", startLineNum+1)

	// This is a verbatim section, so we write it without processing
	tagName, htmlTag, restLine := doc.RenderTagForLine(startLineNum)

	verbatimSectionIndentation := doc.Indentation(startLineNum)
	indentStr := strings.Repeat(" ", verbatimSectionIndentation)

	startOfNextBlock := 0
	lastNonEmptyLineNum := 0
	minimumIndentation := doc.Indentation(startLineNum + 1)

	// Loop until the end of the document or until we find a line with less indentation
	// Blank lines are assumed to pertain to the verbatim section
	for i := startLineNum + 1; !doc.AtEOF(i); i++ {

		startOfNextBlock = i

		// This is the indentation of the text in the verbatim section
		// We do not require that it is left-alligned, but calculate its offset
		thisLineIndentation := doc.Indentation(i)

		// If the line is non-blank
		if len(doc.Line(i)) > 0 {

			// Break the loop if indentation of this line is less or equal than pre section
			if thisLineIndentation <= verbatimSectionIndentation {
				// This line is part of th enext block
				break
			}

			// Update the number of the last line of the verbatim section
			lastNonEmptyLineNum = i

			// Update the minimum indentation in the whole section
			if thisLineIndentation < minimumIndentation {
				minimumIndentation = thisLineIndentation
			}

		}

	}

	for i := startLineNum + 1; i <= lastNonEmptyLineNum; i++ {

		thisIndentationStr := ""
		if doc.Indentation(i)-minimumIndentation > 0 {
			thisIndentationStr = strings.Repeat(" ", doc.Indentation(i)-minimumIndentation)
		}

		if i == startLineNum+1 && i == lastNonEmptyLineNum {
			doc.renderer.WriteString(fmt.Sprintf("\n%v%v%v%v", indentStr, string(htmlTag), string(restLine), string(doc.Line(i))))
			// As a very common special case, if there was a <code> in the same line as <pre>, write the end tag too
			if bytes.HasPrefix(restLine, []byte("<code")) {
				doc.renderer.WriteString(fmt.Sprintf("</code></%v>\n\n", string(tagName)))
			} else {
				doc.renderer.WriteString(fmt.Sprintf("</%v>\n\n", string(tagName)))
			}
		} else if i == startLineNum+1 {
			// Write the first line
			doc.renderer.WriteString(fmt.Sprintf("\n%v%v%v%v\n", indentStr, string(htmlTag), string(restLine), string(doc.Line(i))))

		} else if i == lastNonEmptyLineNum {
			// Write the end tag
			// As a very common special case, if there was a <code> in the same line as <pre>, write the end tag too
			if bytes.HasPrefix(restLine, []byte("<code")) {
				doc.renderer.WriteString(fmt.Sprintf("%v%v</code></%v>\n\n", thisIndentationStr, string(doc.Line(i)), string(tagName)))
			} else {
				doc.renderer.WriteString(fmt.Sprintf("%v%v</%v>\n\n", thisIndentationStr, string(doc.Line(i)), string(tagName)))
			}

		} else {
			// Write the verbatim line
			doc.renderer.WriteString(fmt.Sprintf("%v%v\n", thisIndentationStr, string(doc.Line(i))))

		}

	}

	log.Debugw("ProcessVerbatim", "startOfNextBlock", startOfNextBlock+1)
	return startOfNextBlock

}

func (doc *Document) processD2(startLineNum int) int {
	log.Debugw("ProcessD2 enter", "line", startLineNum+1)

	line := doc.Line(startLineNum)

	// Sanity check
	if !lineStartsWithD2(line) {
		return startLineNum
	}

	// This is the indentation of the line specifying the section
	// The content should have higher indentation
	verbatimSectionIndentation := doc.Indentation(startLineNum)

	// Will hold the line number for the next block
	startOfNextBlock := 0

	// This will hold the string with the text lines for D2 drawing
	var d2String []byte

	// Loop until the end of the document or until we find a line with less or equal indentation
	// Blank lines are assumed to pertain to the verbatim section
	for i := startLineNum + 1; !doc.AtEOF(i); i++ {

		startOfNextBlock = i

		// This is the indentation of the text in the verbatim section
		// We do not require that it is left-alligned, but calculate its offset
		thisLineIndentation := doc.Indentation(i)

		// If the line is non-blank
		if len(doc.Line(i)) > 0 {

			// Break the loop if indentation of this line is less or equal than pre section
			if thisLineIndentation <= verbatimSectionIndentation {
				// This line is part of the next block
				break
			}

			ind := bytes.Repeat([]byte(" "), doc.Indentation(i)-verbatimSectionIndentation)
			d2String = append(d2String, ind...)
			d2String = append(d2String, doc.Line(i)...)
			d2String = append(d2String, '\n')

		}

	}

	// Create the SVG from the D2 description
	ruler, err := textmeasure.NewRuler()
	if err != nil {
		log.Fatalw("processD2", "line", startLineNum)
	}

	defaultLayout := func(ctx context.Context, g *d2graph.Graph) error {
		return d2dagrelayout.Layout(ctx, g, nil)
	}
	diagram, _, err := d2lib.Compile(context.Background(), string(d2String), &d2lib.CompileOptions{
		Layout: defaultLayout,
		Ruler:  ruler,
	})
	if err != nil {
		log.Fatalw("processD2", "line", startLineNum)
	}
	out, err := d2svg.Render(diagram, &d2svg.RenderOpts{
		Pad:     d2svg.DEFAULT_PADDING,
		ThemeID: d2themescatalog.NeutralDefault.ID,
	})
	if err != nil {
		log.Fatalw("processD2", "line", startLineNum)
	}
	// err = os.WriteFile(filepath.Join("out.svg"), out, 0600)
	// if err != nil {
	// 	log.Fatalw("processD2", "line", startLineNum)
	// }

	// Write the diagram as an HTML comment to enhance readability
	doc.renderer.WriteString("<!--Original D2 diagram definition\n")
	doc.renderer.WriteString(string(d2String))
	doc.renderer.WriteString("-->\n")

	doc.renderer.WriteString("\n")
	doc.renderer.Write(out)
	doc.renderer.WriteString("\n")

	log.Debugw("ProcessD2 exit", "startOfNextBlock", startOfNextBlock+1)
	return startOfNextBlock

}

func (doc *Document) ProcessSectionTag(startLineNum int) int {
	// Section starts with a tag spec. Process the tag and
	// advance the line pointer appropriately
	tagName, htmlTag, restLine := doc.RenderTagForLine(startLineNum)
	thisIndentation := doc.Indentation(startLineNum)

	// Write the first line, wrapping its text in a <p> if not empty and if the tag is not a <p> itself
	// We add a blank line before, to make the output more readable
	// if len(restLine) > 0 && tagName != "p" {
	// 	restLine = "<p>" + restLine + "</p>"
	// }
	doc.renderer.WriteString(fmt.Sprintf("\n%v%v%v\n", doc.indentStr(startLineNum), string(htmlTag), string(restLine)))

	// If the next non-blank line is indented the same, we write the end tag and return
	// Otherwise, we start and process a new indented block

	// Skip all the blank lines
	nextLineNum := doc.skipBlankLines(startLineNum + 1)
	if doc.AtEOF(nextLineNum) {
		log.Debugf("EOF reached at line %v", startLineNum+1)
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
		doc.renderer.WriteString(fmt.Sprintln())
	} else {
		doc.renderer.WriteString(fmt.Sprintf("%v</%v>\n\n", doc.indentStr(startLineNum), string(tagName)))

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
		log.Debugf("EOF reached at line %v\n", startLineNum)
		return startLineNum
	}

	// Calculate indentation of the first line to process
	// This is going to be the indentation of the current block to process
	thisBlockIndentation := doc.Indentation(startLineNum)

	// In this loop we process all paragraphs at the same indentation or higher
	// We stop processing the block when the indentation decreases or we reach the EOF
	for currentLineNum = startLineNum; !doc.AtEOF(currentLineNum); {

		currentLine := doc.Line(currentLineNum)
		currentLineIndentation := doc.Indentation(currentLineNum)

		// If the line is empty, just go to the next one
		if len(currentLine) == 0 {
			currentLineNum++
			continue
		}

		// This is just for debugging, when printing the start of a line instead of the whole content
		prefixLen := len(currentLine)
		if prefixLen > 4 {
			prefixLen = 4
		}
		log.Debugw("ProcessBlock", "line", currentLineNum+1, "indent", currentLineIndentation, "l", string(currentLine[:prefixLen]))

		// If the line has less indentation than the block, stop processing this block
		if currentLineIndentation < thisBlockIndentation {
			break
		}

		// If indentation is greater, we start a new Block
		if currentLineIndentation > thisBlockIndentation {
			currentLineNum = doc.ProcessBlock(currentLineNum)
			continue
		}

		// A D2 drawing
		if lineStartsWithD2(currentLine) {
			currentLineNum = doc.processD2(currentLineNum)
			continue
		}

		// A verbatim section that is not processed
		if lineStartsWithPre(currentLine) {
			currentLineNum = doc.processVerbatim(currentLineNum)
			continue
		}

		// Headers have some special processing
		if doc.lineStartsWithHeaderTag(currentLineNum) {
			currentLineNum = doc.processHeaderParagraph(currentLineNum)
			continue
		}

		// Lists have also some special processing
		if doc.lineStartsWithList(currentLineNum) {
			currentLineNum = doc.ProcessList(currentLineNum)
			continue
		}

		// Any other tag which starts a section, like div, p, section, article, ...
		if doc.lineStartsWithSectionTag(currentLineNum) {
			currentLineNum = doc.ProcessSectionTag(currentLineNum)
			continue
		}

		// A line without any section tag starts a paragraph block
		currentLineNum = doc.processParagraph(currentLineNum)

	}

	return currentLineNum

}

// processWatch checks periodically if an input file (inputFileName) has been modified, and if so
// it processes the file and writes the result to the output file (outputFileName)
func processWatch(inputFileName string, outputFileName string, sugar *zap.SugaredLogger) error {

	var old_timestamp time.Time
	var current_timestamp time.Time

	// Loop forever
	for {

		// Get the modified timestamp of the input file
		info, err := os.Stat(inputFileName)
		if err != nil {
			return err
		}
		current_timestamp = info.ModTime()

		// If current modified timestamp is newer than the previous timestamp, process the file
		if old_timestamp.Before(current_timestamp) {

			// Update timestamp for the next cycle
			old_timestamp = current_timestamp

			fmt.Println("************Processing*************")

			// Parse the document
			b, err := NewDocumentFromFileBytes(inputFileName)
			if err != nil {
				return err
			}

			// Render to HTML
			html := b.ToHTML()

			// And write the new version of the HTML
			err = os.WriteFile(outputFileName, []byte(html), 0664)
			if err != nil {
				return err
			}
		}

		// Check again in one second
		time.Sleep(1 * time.Second)

	}
}

// NewDocumentFromFile reads a file and preprocesses it in memory
func NewDocumentFromFileBytes(fileName string) (*Document, error) {

	// Read the whole file into memory
	file, err := os.Open(fileName)
	if err != nil {
		log.Fatalln(err)
	}
	defer file.Close()

	// Process the file one line at a time, creating a Document object in memory
	linescanner := bufio.NewScanner(file)
	b, err := NewDocument(linescanner)
	return b, err
}

// process is the main entry point of the program
func process(c *cli.Context) error {

	// Default input file name
	var inputFileName = "index.rite"

	// Output file name command line parameter
	outputFileName := c.String("output")

	// Dry run
	dryrun := c.Bool("dryrun")

	debug = c.Bool("debug")

	norespec = c.Bool("norespec")

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

	log = z.Sugar()
	defer log.Sync()

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

	// This is useful for development.
	// If the user specified to watch, loop forever processing the input file when modified
	if c.Bool("watch") {
		err = processWatch(inputFileName, outputFileName, log)
		return err
	}

	// Preprocess the input file
	b, err := NewDocumentFromFileBytes(inputFileName)
	if err != nil {
		return err
	}

	// Print stats data if requested
	if debug {
		b.printPreprocessStats()
	}

	// // Generate the HTML from the preprocessed data
	html := b.ToHTML()

	// Do nothing if flag dryrun was specified
	if dryrun {
		return nil
	}

	// Write the HTML to the output file
	err = os.WriteFile(outputFileName, []byte(html), 0664)
	if err != nil {
		return err
	}

	return nil
}

func main() {

	app := &cli.App{
		Name:     "rite",
		Version:  "v0.03",
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
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "write html to `FILE` (default is input file name with extension .html)",
			},
			&cli.BoolFlag{
				Name:    "norespec",
				Aliases: []string{"p"},
				Usage:   "do not generate using respec semantics, just a plain HTML file",
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
		fmt.Println("Error:", err)
	}

}
