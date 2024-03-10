package rite

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

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
			stdlog.Panicf("attemping to write something not a string, int, rune, []byte or byte: %T", s)
		}
	}
}

func (r *ByteRenderer) Renderln(inputs ...any) {
	r.Render(inputs...)
	r.Render('\n')
}

// CloneBytes returns a copy of the buffer contents, so the returned copy is owned by the caller
func (r *ByteRenderer) CloneBytes() []byte {
	return bytes.Clone(r.Bytes())
}

type Text struct {
	Indentation int
	LineNumber  int
	Content     []byte
}

// String represents the Text with the 20 first characters
func (para *Text) String() string {
	// This is helpful for debugging
	if para == nil {
		return "<nil>"
	}

	numChars := 20
	if len(para.Content) < numChars {
		numChars = len(para.Content)
	}

	return strings.Repeat(" ", para.Indentation) + string(para.Content[:numChars])
}

func HasPrefix(line []byte, pre string) bool {
	return bytes.HasPrefix(line, []byte(pre))
}

func TrimLeft(line []byte, s byte) (int, []byte) {
	for i, c := range line {
		if c != s {
			return i, line[i:]
		}
	}
	return len(line), nil
}

func EncodeOnPlaceWithUnderscore(line []byte) []byte {
	for i, c := range line {
		if c == ' ' || c == ':' {
			line[i] = '_'
		}
	}
	return line
}

func SkipWhiteSpace(line []byte) []byte {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return line[i:]
		}
	}
	return nil
}

func ReadWord(line []byte) (word []byte, rest []byte) {

	// If no blank space found, return the whole input line
	indexSpace := bytes.IndexByte(line, ' ')
	if indexSpace == -1 {
		return line, nil
	}

	// Otherwise, return the first word and the remaining text in the line
	word = line[:indexSpace]
	line = line[indexSpace+1:]

	line = SkipWhiteSpace(line)
	return word, line

}

func ReadTagName(tagSpec []byte) (tagName []byte, rest []byte) {
	return ReadWord(tagSpec)
}

func ReadQuotedWords(workingTagSpec []byte) (word []byte, rest []byte) {

	// The first character can be the quotation mark
	quote := workingTagSpec[0]

	// The identifier can be enclosed in single or double quotes if there are spaces
	if quote != '"' && quote != '\'' {
		return ReadWord(workingTagSpec)
	}

	workingTagSpec = workingTagSpec[1:]
	for i, c := range workingTagSpec {
		if c == quote {
			return workingTagSpec[:i], workingTagSpec[i+1:]
		}
	}

	fmt.Printf("malformed tag: %s\n", workingTagSpec)
	panic("malformed tag")

}

func ReadTagAttrKey(tagSpec []byte) (Attribute, []byte) {
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
	workingTagSpec = SkipWhiteSpace(workingTagSpec)
	if len(workingTagSpec) == 0 || workingTagSpec[0] != '=' {
		return attr, workingTagSpec
	}

	// Skip whitespace after the '=' sign
	workingTagSpec = SkipWhiteSpace(workingTagSpec[1:])

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
