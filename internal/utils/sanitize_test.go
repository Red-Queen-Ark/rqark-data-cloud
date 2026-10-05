package utils

import (
	"reflect"
	"testing"
)

func TestStripNullCharsRemovesEveryNUL(t *testing.T) {
	got := StripNullChars("a\x00b\x00\x00c")
	want := "abc"
	if got != want {
		t.Errorf("StripNullChars = %q, want %q", got, want)
	}
}

func TestStripNullCharsLeavesCleanStringAlone(t *testing.T) {
	s := "plain text with \t and \n"
	if got := StripNullChars(s); got != s {
		t.Errorf("StripNullChars = %q, want %q", got, s)
	}
}

func TestStripNullCharsWalksNestedValues(t *testing.T) {
	in := map[string]any{
		"content": []any{
			map[string]any{"text": " \x00N\x00A\x00M\x00E\x00"},
			"tail\x00",
		},
		"count":   3,
		"flag":    true,
		"missing": nil,
	}
	want := map[string]any{
		"content": []any{
			map[string]any{"text": " NAME"},
			"tail",
		},
		"count":   3,
		"flag":    true,
		"missing": nil,
	}

	if got := StripNullChars(in); !reflect.DeepEqual(got, want) {
		t.Errorf("StripNullChars = %#v, want %#v", got, want)
	}
}
