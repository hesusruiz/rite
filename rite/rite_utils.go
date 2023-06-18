package rite

import "bytes"

func HasPrefix(line []byte, pre string) bool {
	return bytes.HasPrefix(line, []byte(pre))
}

// convertNewlines converts "\r" and "\r\n" in s to "\n".
// The conversion happens in place, but the resulting slice may be shorter.
func convertNewlines(s []byte) []byte {
	for i, c := range s {
		if c != '\r' {
			continue
		}

		src := i + 1
		if src >= len(s) || s[src] != '\n' {
			s[i] = '\n'
			continue
		}

		dst := i
		for src < len(s) {
			if s[src] == '\r' {
				if src+1 < len(s) && s[src+1] == '\n' {
					src++
				}
				s[dst] = '\n'
			} else {
				s[dst] = s[src]
			}
			src++
			dst++
		}
		return s[:dst]
	}
	return s
}
