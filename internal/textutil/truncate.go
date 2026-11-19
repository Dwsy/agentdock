package textutil

import "unicode/utf8"

type Truncation struct {
	Text      string
	Truncated bool
	Omitted   int
}

func SafeTruncateString(value string, maxBytes int) Truncation {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return Truncation{Text: value}
	}
	cut := safeUTF8BoundaryString(value, maxBytes)
	return Truncation{Text: value[:cut], Truncated: true, Omitted: len(value) - cut}
}

// SafeTruncateTailString 从结尾保留最多 maxBytes 字节，并在 UTF-8 边界处对齐。
func SafeTruncateTailString(value string, maxBytes int) Truncation {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return Truncation{Text: value}
	}
	start := len(value) - maxBytes
	for start > 0 && start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	omitted := start
	return Truncation{Text: value[start:], Truncated: true, Omitted: omitted}
}

func SafeTruncateBytes(data []byte, maxBytes int) Truncation {
	if maxBytes <= 0 || len(data) <= maxBytes {
		return Truncation{Text: string(data)}
	}
	cut := safeUTF8Boundary(data, maxBytes)
	return Truncation{Text: string(data[:cut]), Truncated: true, Omitted: len(data) - cut}
}

func safeUTF8BoundaryString(s string, maxBytes int) int {
	if maxBytes <= 0 || maxBytes >= len(s) {
		return len(s)
	}
	// Trim at most one trailing multi-byte rune without scanning from byte 0 unless invalid UTF-8 exists earlier.
	cut := maxBytes
	start := maxBytes - 1
	for start > 0 && maxBytes-start < utf8.UTFMax && !utf8.RuneStart(s[start]) {
		start--
	}
	if utf8.RuneStart(s[start]) && s[start] >= utf8.RuneSelf {
		if r, w := utf8.DecodeRuneInString(s[start:maxBytes]); r == utf8.RuneError && w == 1 {
			cut = start
		}
	}
	if utf8.ValidString(s[:cut]) {
		return cut
	}
	for i := 0; i < cut; {
		r, w := utf8.DecodeRuneInString(s[i:cut])
		if r == utf8.RuneError && w == 1 {
			return i
		}
		i += w
	}
	return 0
}

func safeUTF8Boundary(data []byte, maxBytes int) int {
	if maxBytes <= 0 || maxBytes >= len(data) {
		return len(data)
	}
	cut := maxBytes
	start := maxBytes - 1
	for start > 0 && maxBytes-start < utf8.UTFMax && !utf8.RuneStart(data[start]) {
		start--
	}
	if utf8.RuneStart(data[start]) && data[start] >= utf8.RuneSelf {
		if r, w := utf8.DecodeRune(data[start:maxBytes]); r == utf8.RuneError && w == 1 {
			cut = start
		}
	}
	if utf8.Valid(data[:cut]) {
		return cut
	}
	for i := 0; i < cut; {
		r, w := utf8.DecodeRune(data[i:cut])
		if r == utf8.RuneError && w == 1 {
			return i
		}
		i += w
	}
	return 0
}
