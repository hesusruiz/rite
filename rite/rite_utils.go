package rite

import (
	"bytes"
	"fmt"
)

func HasPrefix(line []byte, pre string) bool {
	return bytes.HasPrefix(line, []byte(pre))
}

func trimLeft(line []byte, s byte) []byte {
	for i, c := range line {
		if c != s {
			return line[i:]
		}
	}
	return nil
}

func encodeOnPlaceWithUnderscore(line []byte) []byte {
	for i, c := range line {
		if c == ' ' || c == ':' {
			line[i] = '_'
		}
	}
	return line
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

func readQuotedWords(workingTagSpec []byte) (word []byte, rest []byte) {

	// The first character must be the quotation mark
	quote := workingTagSpec[0]

	workingTagSpec = workingTagSpec[1:]
	for i, c := range workingTagSpec {
		if c == quote {
			return workingTagSpec[:i], workingTagSpec[i+1:]
		}
	}

	fmt.Printf("malformed tag: %s\n", workingTagSpec)
	panic("malformed tag")

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
				attr.Val = workingTagSpec[:i]
				return attr, workingTagSpec[i+1:]
			}
		}
	default:
		fmt.Printf("malformed tag: %s\n", workingTagSpec)
		panic("malformed tag")

	}
	return attr, workingTagSpec
}
