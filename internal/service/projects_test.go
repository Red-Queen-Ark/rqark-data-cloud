package service

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeProjectPathKeepsARelativePath(t *testing.T) {
	cases := map[string]string{
		"acme/api":            "acme/api",
		"acme/api/":           "acme/api",
		"acme//api":           "acme/api",
		"acme/./api":          "acme/api",
		"  acme/api  ":        "acme/api",
		"acme":                "acme",
		"deep/nested/project": "deep/nested/project",
	}

	for input, want := range cases {
		got, err := normalizeProjectPath(input)
		if err != nil {
			t.Errorf("normalizeProjectPath(%q) returned %v, want %q", input, err, want)
			continue
		}
		if got != want {
			t.Errorf("normalizeProjectPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeProjectPathRejectsPathsOutsideTheVolume(t *testing.T) {
	cases := map[string]string{
		"empty":           "",
		"blank":           "   ",
		"absolute":        "/workspace/acme",
		"parent":          "../acme",
		"nested parent":   "acme/../../etc",
		"backslash":       `acme\api`,
		"nul":             "acme\x00api",
		"dot":             ".",
		"only separators": "///",
	}

	for name, input := range cases {
		if _, err := normalizeProjectPath(input); err == nil {
			t.Errorf("%s: normalizeProjectPath(%q) accepted the path, want a rejection", name, input)
		} else {
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Errorf("%s: error = %T, want *ValidationError", name, err)
			}
		}
	}
}

func TestNormalizeProjectPathEnforcesTheColumnWidth(t *testing.T) {
	tooLong := strings.Repeat("a", maxProjectPathLen+1)
	if _, err := normalizeProjectPath(tooLong); err == nil {
		t.Fatal("an over-long path was accepted")
	}
}

func TestNormalizeProjectNameTrimsTheName(t *testing.T) {
	got, err := normalizeProjectName("  Acme API \n")
	if err != nil {
		t.Fatalf("normalizeProjectName: %v", err)
	}
	if got != "Acme API" {
		t.Errorf("name = %q, want %q", got, "Acme API")
	}
}

func TestNormalizeProjectNameRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"empty":             "",
		"blank":             "  ",
		"control character": "acme\x01api",
		"too long":          strings.Repeat("a", maxProjectNameLen+1),
		"nul":               "acme\x00api",
	}

	for name, input := range cases {
		if _, err := normalizeProjectName(input); err == nil {
			t.Errorf("%s: normalizeProjectName(%q) accepted the name, want a rejection", name, input)
		}
	}
}

func TestNormalizeProjectNameCountsRunesNotBytes(t *testing.T) {
	// 64 two-byte characters are 128 bytes: the column counts characters, so
	// this must be accepted.
	name := strings.Repeat("項", maxProjectNameLen)
	if _, err := normalizeProjectName(name); err != nil {
		t.Fatalf("normalizeProjectName(%d runes): %v", maxProjectNameLen, err)
	}
}
