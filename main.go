// Copyright 2023 Jesus Ruiz. All rights reserved.
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"fmt"
	"html"
	"os"
	"path"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/hesusruiz/rite/sliceedit"
	"github.com/hesusruiz/vcutils/yaml"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"

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
	"code", "b", "i", "hr", "em", "strong", "small", "s",
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
		return nil, fmt.Errorf("addHeading, line %v: expected h1, received %s", o.doc.lastLine, tagName)
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

	_, startTag, _, rest := tag.Render()

	newHeading := &Heading{}
	switch tagName {
	case "h1":
		o.subheadings = append(o.subheadings, newHeading)
		r := fmt.Appendf([]byte{}, "%s<span class='secno'>%d</span> %s", startTag, len(o.subheadings), rest)
		return r, nil
	case "h2":
		if len(o.subheadings) == 0 {
			return nil, fmt.Errorf("addHeading, line %v: adding '%v' but no 'h1' exists", o.doc.lastLine, tagName)
		}
		l1 := o.subheadings[len(o.subheadings)-1]
		l1.subheadings = append(l1.subheadings, newHeading)

		r := fmt.Appendf([]byte{}, "%s<span class='secno'>%d.%d</span> %s", startTag, len(o.subheadings), len(l1.subheadings), rest)
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
		r := fmt.Appendf([]byte{}, "%s<span class='secno'>%d.%d.%d</span> %s", startTag, len(o.subheadings), len(l1.subheadings), len(l1.subheadings), rest)
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
	number      int
	indentation int
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

func (t *TagStruct) Indentation() int {
	return t.indentation
}

func (t *TagStruct) IndentStr() string {
	return strings.Repeat(" ", t.indentation)
}

func (t *TagStruct) Render() (tagName []byte, startTag []byte, endTag []byte, rest []byte) {
	// Sanity check
	if t == nil {
		return nil, nil, nil, nil
	}

	switch string(t.Tag) {

	case "pre":
		// Handle the 'pre' tag, with special case when the section started with '<pre><code>
		startTag = fmt.Appendf(startTag, "<pre")
		if bytes.HasPrefix(t.RestLine, []byte("<code")) {
			endTag = fmt.Appendf(endTag, "</code>")
		}
		endTag = fmt.Appendf(endTag, "</pre>")

	case "x-code":
		// Handle the 'x-code' special tag
		startTag = fmt.Appendf(startTag, "<pre")
		endTag = fmt.Appendf(endTag, "</code></pre>")

	case "x-img":
		// Handle the 'x-img' special tag
		startTag = fmt.Appendf(startTag, "<figure><img")
		endTag = fmt.Appendf(endTag, "<figcaption>%s</figcaption></figure>", t.RestLine)

	default:
		startTag = fmt.Appendf(startTag, "<%s", t.Tag)
		endTag = fmt.Appendf(endTag, "</%s>", t.Tag)

	}

	if len(t.Id) > 0 {
		startTag = fmt.Appendf(startTag, " id='%s'", t.Id)
	}
	if len(t.Class) > 0 {
		startTag = fmt.Appendf(startTag, " class='%s'", t.Class)
	}
	if len(t.Src) > 0 {
		startTag = fmt.Appendf(startTag, " src='%s'", t.Src)
	}
	if len(t.Href) > 0 {
		startTag = fmt.Appendf(startTag, " href='%s'", t.Href)
	}
	if len(t.StdFields) > 0 {
		startTag = fmt.Appendf(startTag, " %s", t.StdFields)
	}

	restLine := t.RestLine

	// Handle the special cases
	switch string(t.Tag) {
	case "section":
		startTag = fmt.Appendf(startTag, ">")
		if len(t.RestLine) > 0 {
			startTag = fmt.Appendf(startTag, "<h2>%s</h2>\n", t.RestLine)
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

	return t.Tag, startTag, endTag, restLine

}

type LineStruct struct {
	number      int
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

func (doc *Document) Len() int {
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
	tagFields, err := doc.preprocessTagSpec(lineNum)
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

func (doc *Document) StartTagType(lineNum int) tagType {
	if !doc.ValidLineNum(lineNum) {
		return -1
	}
	if doc.theLines[lineNum].startTag == nil {
		return PARAGRAPH
	}
	return doc.theLines[lineNum].startTag.Typ
}

// *******************************************
// *******************************************
// *******************************************

func trimLeft(input []byte, c byte) []byte {
	for len(input) > 0 && input[0] == c {
		input = input[1:]
	}
	if len(input) == 0 {
		return nil
	}
	return input
}

func lineStartsWithVerbatim(line []byte) bool {
	if lineStartsWithPre(line) || lineStartsWithD2(line) || lineStartsWithCode(line) {
		return true
	}
	return false
}

// lineStartsWithPre returns true if the line starts with '<pre'
func lineStartsWithPre(line []byte) bool {
	return bytes.HasPrefix(line, []byte("<pre"))
}

// lineStartsWithCode returns true if the line starts with '<x-code'
func lineStartsWithCode(line []byte) bool {
	return bytes.HasPrefix(line, []byte("<x-code"))
}

// lineStartsWithD2 returns true if the line starts with '<x-diagram .d2>'
func lineStartsWithD2(line []byte) bool {
	return bytes.HasPrefix(line, []byte("<x-diagram .d2>"))
}

// lineStartsWithTag returns true if the line starts with a start tags character '<'
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

// lineStartsWithSectionTag returns true if the line
// starts with one of the block tags that we recognize.
func (doc *Document) lineStartsWithSectionTag(lineNum int) bool {

	// Decompose the tag into its elements
	tagFields, _ := doc.preprocessTagSpec(lineNum)

	// Return false if there is no tag or it is in the sets that we know should not start a section
	// For example, void elements
	if tagFields == nil || isNoSectionElement(tagFields.Tag) || contains(voidElements, tagFields.Tag) {
		return false
	}

	return true

}

func (doc *Document) lineStartsWithListTag(lineNum int) bool {
	line := doc.Line(lineNum)
	return bytes.HasPrefix(line, []byte("<ol")) || bytes.HasPrefix(line, []byte("<ul"))
}

// *******************************************
// *******************************************
// *******************************************
// *******************************************

func (doc *Document) readLine(s *bufio.Scanner) (*LineStruct, []byte) {

	// Get a rawLine from the file
	rawLine := bytes.Clone(s.Bytes())

	// // Strip blanks at the beginning of the line and calculate indentation based on the difference in length
	// // We do not support other whitespace like tabs
	// line := trimLeft(rawLine, ' ')
	// indentation := len(rawLine) - len(line)

	// Calculate the line number
	lineNum := doc.Len()
	doc.lastLine = lineNum + 1

	// Create and add the line struct
	theLine := &LineStruct{}
	theLine.number = lineNum
	doc.theLines = append(doc.theLines, theLine)

	return theLine, rawLine

}

func (doc *Document) preprocessYAMLHeader(s *bufio.Scanner) error {
	var err error

	// We need at least one line
	if !s.Scan() {
		return fmt.Errorf("no YAML metadata found")
	}

	// Get a line from the file
	_, line := doc.readLine(s)

	// We accept YAML data only at the beginning of the file
	if !bytes.HasPrefix(line, []byte("---")) {
		return fmt.Errorf("no YAML metadata found")
	}

	// Build a string with all subsequent lines up to the next "---"
	var yamlString strings.Builder
	var endYamlFound bool

	for s.Scan() {

		// Get a line from the file
		_, line := doc.readLine(s)

		if bytes.HasPrefix(line, []byte("---")) {
			endYamlFound = true
			break
		}

		yamlString.Write(line)
		yamlString.WriteString("\n")

	}

	if !endYamlFound {
		return fmt.Errorf("end of file reached but no end of YAML section found")
	}

	// Parse the string that was built as YAML data
	doc.config, err = yaml.ParseYaml(yamlString.String())
	if err != nil {
		log.Fatalw("malformed YAML metadata", "error", err)
	}

	return nil
}

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

	// Process the YAML header if there is one. It should be at the beginning of the file
	err := doc.preprocessYAMLHeader(s)
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
		line := trimLeft(rawLine, ' ')
		indentation := len(rawLine) - len(line)

		// Calculate the line number
		lineNum := doc.Len()
		doc.lastLine = lineNum + 1

		// Create and add the line struct
		theLine := &LineStruct{}
		theLine.indentation = indentation
		theLine.line = line
		doc.theLines = append(doc.theLines, theLine)

		// If the line is empty we are done
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

			// We have already exited the verbatim area
			insideVerbatim = false
		}

		if lineStartsWithVerbatim(line) {
			// The verbatim area is indicated by a <pre> or diagram D2 tag
			insideVerbatim = true

			// Remember the indentation of the tag
			// Verbatim content has to be indented (indentation > indentationVerbatim)
			indentationVerbatim = indentation

		}

		// We ignore any line starting with an end tag
		if bytes.HasPrefix(line, []byte("</")) {
			doc.theLines[lineNum].startTag = nil
			doc.theLines[lineNum].line = nil
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
		tagFields, _ := doc.preprocessTagSpec(lineNum)
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
	if err := s.Err(); err != nil {
		log.Errorw("error scanning the input file", "err", err)
	}

	return doc, nil

}

// preprocessTagSpec returns a structure with the tag fields of the tag at the beginning of the line.
// It returns nil and an error if the line does not start with a tag.
func (doc *Document) preprocessTagSpec(lineNum int) (*TagStruct, error) {
	var tagSpec, restLine []byte

	rawLine := doc.Line(lineNum)

	// Sanity check
	if len(rawLine) == 0 || rawLine[0] != startHTMLTag {
		return nil, fmt.Errorf("preprocessTagSpec, line %d: line does not start with a tag", doc.lastLine)
	}

	t := &TagStruct{}

	t.number = lineNum
	t.indentation = doc.Indentation(lineNum)

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
		if len(f) < 2 {
			return nil, fmt.Errorf("preprocessTagSpec, line %d: Length of attributes must be greater than 1", doc.lastLine)
		}

		switch f[0] {
		case '#':
			// Shortcut for id="xxxx"
			// Only the first id attribute is used, others are ignored
			if len(t.Id) == 0 {
				t.Id = f[1:]
			}
		case '.':
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
			// Shortcut for src="xxxx"
			// Only the first attribute is used
			if len(t.Src) == 0 {
				t.Src = f[1:]
			}
		case '-':
			// Shortcut for href="xxxx"
			// Only the first attribute is used
			if len(t.Href) == 0 {
				t.Href = f[1:]
			}
		case ':':
			// Special attribute "type" for item classification and counters
			// Only the first attribute is used
			if len(t.Bucket) == 0 {
				t.Bucket = f[1:]
			}
		case '=':
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

func (doc *Document) printPreprocessStats() {
	fmt.Printf("Number of lines: %v\n", doc.Len())
	fmt.Println()
	fmt.Printf("Number of ids: %v\n", len(doc.ids))
	for k, v := range doc.figs {
		fmt.Printf("  %v: %v\n", k, v)
	}
}

// ***************************************************************
// ***************************************************************
// ***************************************************************

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
	return lineNum == EOF || lineNum >= doc.Len()
}

func (doc *Document) Indentation(lineNum int) int {
	if doc.ValidLineNum(lineNum) {
		if doc.theLines[lineNum] == nil {
			log.Panicf("invalid line struct at line %d", lineNum)
		}
		return doc.theLines[lineNum].indentation
	} else {
		log.Panicf("invalid line number %d", lineNum)
		return 0
	}
}

// skipBlankLines returns the line number of the first non-blank line,
// starting from the provided line number, or EOF if there are no more blank lines.
// If the start line is non-blank, we return that line.
func (doc *Document) skipBlankLines(lineNumber int) int {
	var trimmedLine []byte

	for i := lineNumber; i < doc.Len(); i++ {

		// Trim all blanks to see if the line is a blank line
		trimmedLine = bytes.TrimSpace(doc.Line(i))

		// Return if non-blank
		if len(trimmedLine) > 0 {
			return i
		}

	}

	// Return the size of the file (one more than the last line number)
	// This is used as an indication that we are at End of File
	return doc.Len()
}

func (doc *Document) Render(inputs ...any) {
	for _, s := range inputs {
		switch v := s.(type) {
		case string:
			doc.renderer.WriteString(v)
		case []byte:
			doc.renderer.Write(v)
		case byte:
			doc.renderer.WriteByte(v)
		case rune:
			doc.renderer.WriteRune(v)
		default:
			log.Fatalf("attemping to write something not a string, []byte or byte: %T", s)
		}
	}
}

func (doc *Document) ToHTML() string {
	// Start processing the main block
	doc.ProcessBlock(0)
	// Run processess that can not be done line by line and return the final HTML
	return doc.postProcess()
}

//go:embed assets
var assets embed.FS

// postProcess performs any process that can only be done after the whole document has been processed,
// like cross references between sections.
// It returns the final document as a string
func (doc *Document) postProcess() string {

	// Initialise the template system
	templateDir := doc.config.String("template", "assets/templates/respec")
	fmt.Println("Using template dir:", templateDir)

	// Parse all templates in the following directories
	t := template.Must(template.ParseFS(assets, templateDir+"/layouts/*"))
	t = template.Must(t.ParseFS(assets, templateDir+"/partials/*"))
	t = template.Must(t.ParseFS(assets, templateDir+"/pages/*"))

	// Get the bibliography for the references.
	// It can be specified in the YAML header or in a separate file.
	// The bibliography in the header has precedence if it exists.
	bibData := doc.config.Map("localBiblio", nil)
	if bibData == nil {

		// Read the bibliography file if it exists
		// First try reading the file specified in the YAML header, otherwise use the default name
		bd, err := yaml.ParseYamlFile(doc.config.String("localBiblioFile", "localbiblio.yaml"))
		if err == nil {
			bibData = bd.Map("")
		}
	}

	// Set the data that will be available for the templates
	var data = map[string]any{
		"Config": doc.config.Data(),
		"Biblio": bibData,
		"HTML":   doc.renderer.String(),
	}

	// Execute the template and store the result in memory
	var out bytes.Buffer
	if err := t.ExecuteTemplate(&out, "index.html.tpl", data); err != nil {
		panic(err)
	}

	// Get the raw HTML where we still have to perform some processing
	rawHtml := out.Bytes()

	// Prepare the buffer for efficient editing operations minimizing allocations
	edBuf := sliceedit.NewBuffer(rawHtml)

	// For all IDs that were detected, store the intented changes
	for idName, idNumber := range doc.ids {
		searchString := "{#" + idName + ".num}"
		newValue := fmt.Sprint(idNumber)
		edBuf.ReplaceAllString(searchString, newValue)
	}

	// Replace the HTML escaped codes
	edBuf.ReplaceAllString("\\<", "&lt")
	edBuf.ReplaceAllString("\\>", "&gt")

	// Apply the changes to the buffer and get the HTML
	html := edBuf.String()

	return html
}

// processParagraph reads all contiguous lines of a block, unless it encounters some special tag at the beginning
func (doc *Document) processParagraph(startLineNum int) (nextLineNum int) {

	// Process all contiguous lines in the block, writing them without any processing,
	// except for addint the <p> tag at the beginning and at the end.
	// The indentation of all lines will be the same as the first line.
	for nextLineNum = startLineNum; nextLineNum < doc.Len(); nextLineNum++ {

		line := doc.Line(nextLineNum)
		if len(line) == 0 {
			break
		}

		if nextLineNum == startLineNum {
			// The first line starts with a <p> tag
			doc.Render(doc.IndentStr(startLineNum), "<p>", line, '\n')

		} else {
			doc.Render(doc.IndentStr(startLineNum), line, '\n')
		}

	}

	// Write the end </p> tag
	doc.Render(doc.IndentStr(startLineNum), "</p>\n")

	// Return the next line to process
	return nextLineNum

}

// processHeaderParagraph processes the headers
func (doc *Document) processHeaderParagraph(headerLineNum int) int {

	// Get the rendered tag
	tag := doc.StartTagForLine(headerLineNum)
	_, startTag, endTag, restLine := tag.Render()

	// Write everything in a single line, with extra newlines for aesthetics
	doc.Render(tag.IndentStr(), startTag, restLine, endTag, "\n\n")

	// Return the next line number to continue processing
	return headerLineNum + 1

}

func (doc *Document) IndentStr(lineNum int) string {
	return strings.Repeat(" ", doc.Indentation(lineNum))
}

func (doc *Document) ProcessList(startLineNum int) int {
	var currentLineNum int

	// startLineNum should point to the <ul> or <ol> tag.
	// We expect the block to consist of a sequence of "li" elements, each of them can be as complex as needed
	// We first search for the first list element. It is an error if there is none

	log.Debugw("ProcessList enter", "line", startLineNum+1)
	defer log.Debugw("ProcessList exit", "line", startLineNum+1)

	listTag := doc.StartTagForLine(startLineNum)

	// Sanity check: verify that only "ol" or "ul" are accepted
	if listTag == nil {
		log.Fatalw("no tag, expecting lists ol or ul", "line", startLineNum+1)
	}
	if string(listTag.Tag) != "ol" && string(listTag.Tag) != "ul" {
		log.Fatalw("invalid tag, expecting lists ol or ul", "line", startLineNum+1)
	}

	// Calculate the unique list ID, if it was not specified by the user
	listID := listTag.Id
	if len(listID) == 0 {
		listID = fmt.Appendf(listID, "list_%v", startLineNum+1)
	}

	// Prepare for rendering the <li> line
	_, listStartTag, listEndTag, _ := listTag.Render()

	// List items must have indentation greater than the ol/ul tags
	listIndentation := doc.Indentation(startLineNum)

	// Write the list tag line
	log.Debugw("ProcessList start-of-list tag", "line", startLineNum+1)
	doc.Render(listTag.IndentStr(), listStartTag, "\n")

	var itemIndentStr string
	itemIndentation := 0
	listItemNumber := 0

	// Process each of the list items until end of list or end of file
	for currentLineNum = startLineNum + 1; !doc.AtEOF(currentLineNum); {

		// Do nothing if the line is empty
		if len(doc.Line(currentLineNum)) == 0 {
			currentLineNum++
			continue
		}

		// We have found the first item of the list
		line := doc.Line(currentLineNum)

		// Remember the indentation of the first line.
		// Its indentation sets the expected indentation for all other items.
		if itemIndentation == 0 {
			// This is done only once for the whole list
			itemIndentation = doc.Indentation(currentLineNum)
			itemIndentStr = strings.Repeat(" ", itemIndentation)
		}

		// If the line has less or equal indentation than the ol/ul tags, stop processing this block
		if doc.Indentation(currentLineNum) <= listIndentation {
			break
		}

		// We have a line that must be a list item
		var bulletText string

		// Check if line starts with '<li'
		if !bytes.HasPrefix(line, []byte("<li")) {
			log.Fatalf("line %d, this is not a list element: %s", currentLineNum+1, line)
		}

		// This is a list item, increment the counter (items are numbered starting at 1)
		listItemNumber++

		// Decompose the tag in its elements
		itemTag := doc.StartTagForLine(currentLineNum)

		if len(itemTag.Number) > 0 {
			// The user has specified a bullet text to start the list item

			// Overwrite any existing item ID with a calculated one.
			// Create itemID concatenating listID and the user-specified value for this item.
			// Replace the encoded space chars with an underscore.
			itemTag.Id = fmt.Appendf(listID, ".%s", bytes.ReplaceAll(itemTag.Number, []byte("%20"), []byte("_")))

			// the listNumber is the displayed value of the item number.
			// Replace the encoded space chars by the ASCII equivalent
			listNumber := bytes.ReplaceAll(itemTag.Number, []byte("%20"), []byte(" "))

			// Create the bullet text for the item, with itemID as achor and a bold display of listNumber
			bulletText = fmt.Sprintf("<a href='#%s' class='selfref'><b>%s.</b></a>", itemTag.Id, listNumber)

		} else {
			// The user did not specify explicitly a bullet text, but she may have set an explicit item ID.
			// If the user did not specify anything, we calculate the item ID based on the item sequence number.
			if len(itemTag.Id) == 0 {
				itemTag.Id = fmt.Appendf(listID, ".%d", listItemNumber)
			}
		}

		// Build the tag for presentation
		_, itemStartTag, itemEndTag, restLine := itemTag.Render()

		// Write the first line of the list item
		log.Debugw("ProcessList item open tag", "line", currentLineNum+1)
		doc.Render(itemTag.IndentStr(), itemStartTag, bulletText, restLine)

		// Skip all the blank lines after the first line
		currentLineNum = doc.skipBlankLines(currentLineNum + 1)

		// We are finished if we have reached the end of the document
		if doc.AtEOF(currentLineNum) {
			log.Debugf("EOF reached at line %v\n", currentLineNum+1)
			break
		}

		// Each list item can have additional content which should be more indented
		// We wrap that content in a <div></div> section
		if doc.Indentation(currentLineNum) > itemIndentation {
			log.Debugw("ProcessList before ProcessBlock", "line", currentLineNum+1)

			// Process the following lines as a block, inside a <div> section
			doc.Render(itemIndentStr, "<div>\n")
			currentLineNum = doc.ProcessBlock(currentLineNum)
			doc.Render(itemIndentStr, "</div>\n")

			log.Debugw("ProcessList after ProcessBlock", "line", currentLineNum+1)
		}

		// Write the list item end tag
		log.Debugw("ProcessList item close tag", "line", currentLineNum+1)
		doc.Render(itemIndentStr, itemEndTag, '\n')

	}

	// Write the end-of-list tag
	log.Debugw("ProcessList end-of-list tag", "line", startLineNum+1)
	doc.Render(listTag.IndentStr(), listEndTag, "\n\n")

	// Return the line number following the already processed list
	return currentLineNum

}

// processCodeSection renders a '<x-code> section
func (doc *Document) processCodeSection(sectionLineNum int) int {

	// Get the tag which starts the line
	tag := doc.StartTagForLine(sectionLineNum)

	// Get the rendered tag and end tag
	_, startTag, endTag, _ := tag.Render()

	contentFirstLineNum := sectionLineNum + 1
	startOfNextBlock := 0
	contentLastLineNum := 0
	minimumIndentation := doc.Indentation(contentFirstLineNum)

	// We have to calculate the minimum indentation of all the lines in the section.
	// The lines with that minimum indentation will be left aligned when we generate the section.
	// So we have to perform two passes, one to calculate the minimum indentation and th esecond one to
	// generate the section in the HTML with the proper indentation.
	// Blank lines are assumed to pertain to the verbatim section.
	for i := contentFirstLineNum; !doc.AtEOF(i); i++ {

		startOfNextBlock = i

		// This is the indentation of the text in the verbatim section
		// We do not require that it is left-aligned, but calculate its offset
		thisLineIndentation := doc.Indentation(i)

		// If the line is non-blank
		if len(doc.Line(i)) > 0 {

			// Break the loop if indentation of this line is less or equal than the section
			if thisLineIndentation <= tag.Indentation() {
				// This line is part of the next block
				break
			}

			// Update the number of the last line of the verbatim section
			contentLastLineNum = i

			// Update the minimum indentation in the whole section
			if thisLineIndentation < minimumIndentation {
				minimumIndentation = thisLineIndentation
			}

		}

	}

	// Do nothing if section is empty
	if contentLastLineNum == 0 {
		return startOfNextBlock
	}

	// Write a newline to visually separate from the preceding content
	doc.Render('\n')

	for i := contentFirstLineNum; i <= contentLastLineNum; i++ {

		// Calculate and write the indentation for the line
		thisIndentationStr := ""
		if doc.Indentation(i)-minimumIndentation > 0 {
			thisIndentationStr = strings.Repeat(" ", doc.Indentation(i)-minimumIndentation)
		}
		doc.Render(thisIndentationStr)

		// Escape any HTML in the content line
		escapedContentLine := html.EscapeString(string(doc.Line(i)))

		switch {
		case i == contentFirstLineNum && i == contentLastLineNum:
			// Special case: the section has only one line
			// We have to write the start tag for the section, the content line and the end tag for the section in only one line

			// Write the start tag, escaped content line and the end tag
			// This line is indented according to the indentation of the section tag
			doc.Render(tag.IndentStr(), startTag, escapedContentLine, endTag, "\n")

		case i == contentFirstLineNum:
			// We are at the first line of a section with several lines
			// We have to write the start tag for the section, and the first line of content in the same line

			// Write the start tag and escaped content line
			// This first line is indented according to the indentation of the section tag
			doc.Render(tag.IndentStr(), startTag, escapedContentLine)

		case i > contentFirstLineNum && i < contentLastLineNum:
			// We are in the middle of a section with several lines

			// Write the content line, escaped for HTML tags
			// All lines are left aligned
			doc.Render(escapedContentLine)

		case i == contentLastLineNum:
			// We are at the last line of a section with several lines

			// Write the content line, escaped for HTML tags
			doc.Render(escapedContentLine)

			// Write the end tag for the section
			doc.Render(endTag, "\n")

		}

		// Write the endline
		doc.Render("\n")

	}

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

	// Write the diagram as an HTML comment to enhance readability
	doc.Render("<!-- Original D2 diagram definition\n", d2String, " -->\n")

	// Write the SVG inline with the HTML
	doc.Render('\n', out, '\n')

	log.Debugw("ProcessD2 exit", "startOfNextBlock", startOfNextBlock+1)
	return startOfNextBlock

}

func (doc *Document) ProcessSectionTag(sectionLineNum int) int {
	// Section starts with a tag spec. Process the tag and
	// advance the line pointer appropriately
	tag := doc.StartTagForLine(sectionLineNum)
	tagName, startTag, endTag, restLine := tag.Render()

	if debug {
		fmt.Printf("%s(%d)%s\n", tag.IndentStr(), sectionLineNum+1, startTag)
		defer fmt.Printf("%s(%d)%s\n", tag.IndentStr(), sectionLineNum+1, endTag)
	}

	// Write the first line
	doc.Render('\n', tag.IndentStr(), startTag, restLine, '\n')

	// Skip all the blank lines following the section tag
	nextLineNum := doc.skipBlankLines(sectionLineNum + 1)
	if doc.AtEOF(nextLineNum) {
		log.Debugf("EOF reached at line %v", sectionLineNum+1)
		return nextLineNum
	}

	// Start and process an indented block if the next line is more indented than the tag
	nextIndentation := doc.Indentation(nextLineNum)
	if nextIndentation > tag.Indentation() {
		nextLineNum = doc.ProcessBlock(nextLineNum)
	}

	// Write the end tag for the section
	if isVoidElement(tagName) {
		// HTML spec says no end tag should be used
		doc.Render('\n')
	} else {
		doc.Render(tag.IndentStr(), endTag, "\n\n")
	}

	// Return the next line to process
	return nextLineNum

}

// ProcessBlock recursively processes a document taking into account indentation.
// A document is a block and a block is composed of either:
//   - Paragraphs separated by blank lines
//   - Indented blocks, called sections
func (doc *Document) ProcessBlock(blockLineNum int) int {
	var currentLineNum int

	if debug {
		// This is just for debugging, when printing the start of a line instead of the whole content
		prefixLen := len(doc.Line(blockLineNum))
		if prefixLen > 4 {
			prefixLen = 4
		}
		prefix := doc.Line(blockLineNum)[:prefixLen]

		fmt.Printf("%sStartBlock at %d[%s]\n", doc.IndentStr(blockLineNum), blockLineNum+1, prefix)
		defer fmt.Printf("%sEndBlock at %d[%s]\n", doc.IndentStr(blockLineNum), blockLineNum+1, prefix)
	}

	// Calculate indentation of the first line to process
	// This is going to be the indentation of the current block to process
	blockIndentation := doc.Indentation(blockLineNum)

	// In this loop we process all paragraphs at the same indentation or higher
	// We stop processing the block when the indentation decreases or we reach the EOF
	for currentLineNum = blockLineNum; !doc.AtEOF(currentLineNum); {

		// If the line is empty, just go to the next one
		currentLine := doc.Line(currentLineNum)
		if len(currentLine) == 0 {
			currentLineNum++
			continue
		}

		// If the line has less indentation than the block, stop processing this block
		currentLineIndentation := doc.Indentation(currentLineNum)
		if currentLineIndentation < blockIndentation {
			break
		}

		// A D2 drawing
		if lineStartsWithD2(currentLine) {
			currentLineNum = doc.processD2(currentLineNum)
			continue
		}

		// A diagram
		if lineStartsWithCode(currentLine) || lineStartsWithPre(currentLine) {
			currentLineNum = doc.processCodeSection(currentLineNum)
			continue
		}

		// Headers have some special processing
		if doc.lineStartsWithHeaderTag(currentLineNum) {
			currentLineNum = doc.processHeaderParagraph(currentLineNum)
			continue
		}

		// Lists have also some special processing
		if doc.lineStartsWithListTag(currentLineNum) {
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
			b, err := NewDocumentFromFile(inputFileName)
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
func NewDocumentFromFile(fileName string) (*Document, error) {

	// Read the whole file into memory
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
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
	// If the user specified watch, loop forever processing the input file when modified
	if c.Bool("watch") {
		err = processWatch(inputFileName, outputFileName, log)
		return err
	}

	// Preprocess the input file
	b, err := NewDocumentFromFile(inputFileName)
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
		Version:  "v0.05",
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
