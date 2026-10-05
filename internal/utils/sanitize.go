package utils

import "strings"

// nullChar is U+0000, the NUL character. PostgreSQL cannot store it inside
// jsonb: a jsonb string is decoded into text, text is a C string, and a C
// string has no representation for a NUL, so an insert that carries one fails
// with SQLSTATE 22P05 (unsupported Unicode escape sequence).
const nullChar = "\x00"

// StripNullChars returns v with every U+0000 removed from its strings. It
// walks maps, slices and arrays, since a value here is an arbitrary decoded
// JSON body; every other type is returned unchanged.
//
// It exists for tool output. A Windows command such as "wsl --status" writes
// UTF-16LE, and a caller that does not decode it hands us a string holding a
// NUL after every character. Dropping the NULs stores the message as-is
// instead of failing the whole save; the remaining UTF-16 bytes are left for
// the caller to decode, since guessing their encoding is not this function's
// job.
func StripNullChars(v any) any {
	switch value := v.(type) {
	case string:
		if !strings.Contains(value, nullChar) {
			return value
		}
		return strings.ReplaceAll(value, nullChar, "")
	case map[string]any:
		for key, item := range value {
			value[key] = StripNullChars(item)
		}
		return value
	case []any:
		for i, item := range value {
			value[i] = StripNullChars(item)
		}
		return value
	default:
		return v
	}
}
