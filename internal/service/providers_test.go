package service

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateProviderHeadersAcceptsWhatNetHTTPAccepts covers the pairs a client
// may store: a token name, a value with a colon or a space, and no CR or LF that
// would let a stored value start a second header.
func TestValidateProviderHeadersAcceptsWhatNetHTTPAccepts(t *testing.T) {
	cases := []map[string]string{
		nil,
		{},
		{"X-Api-Version": "2024-06-01"},
		{"HTTP-Referer": "https://example.invalid/a?b=c:d"},
		{"X-Empty": ""},
		{"Authorization": "Bearer sk-test"},
	}
	for _, headers := range cases {
		if err := validateProviderHeaders(headers); err != nil {
			t.Errorf("validateProviderHeaders(%v) = %v, want nil", headers, err)
		}
	}
}

// TestValidateProviderHeadersRejectsAPairNetHTTPWouldRefuse covers the pairs
// that would only fail later, when the proxy builds the upstream request.
func TestValidateProviderHeadersRejectsAPairNetHTTPWouldRefuse(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{name: "empty name", headers: map[string]string{"": "x"}, want: "empty header name"},
		{name: "space in name", headers: map[string]string{"X Api": "x"}, want: "invalid header name"},
		{name: "colon in name", headers: map[string]string{"X-Api:": "x"}, want: "invalid header name"},
		{name: "line feed in value", headers: map[string]string{"X-Api": "a\nb"}, want: "line break"},
		{name: "carriage return in value", headers: map[string]string{"X-Api": "a\rb"}, want: "line break"},
		{name: "name too long", headers: map[string]string{strings.Repeat("X", maxProviderHeaderNameLen+1): "x"}, want: "too long"},
		{name: "value too long", headers: map[string]string{"X-Api": strings.Repeat("v", maxProviderHeaderValueLen+1)}, want: "too long"},
	}
	for _, test := range cases {
		err := validateProviderHeaders(test.headers)
		if err == nil {
			t.Errorf("%s: validateProviderHeaders accepted the pair, want an error", test.name)
			continue
		}
		if !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: error = %q, want it to mention %q", test.name, err, test.want)
		}
	}
}

// TestNormalizeVendorAndHeadersKeepsAbsentValuesNull covers the read-back: an
// empty vendor and an empty header set are stored as NULL rather than as ” or
// {}, so a client sees null for a value it never set.
func TestNormalizeVendorAndHeadersKeepsAbsentValuesNull(t *testing.T) {
	if got := normalizeVendor(nil); got != nil {
		t.Errorf("normalizeVendor(nil) = %v, want nil", got)
	}
	if got := normalizeVendor(ptr("")); got != nil {
		t.Errorf("normalizeVendor(\"\") = %v, want nil", got)
	}
	if got := normalizeVendor(ptr("   ")); got != nil {
		t.Errorf("normalizeVendor(\"   \") = %v, want nil", got)
	}
	if got := normalizeVendor(ptr(" moonshot ")); got == nil || *got != "moonshot" {
		t.Errorf("normalizeVendor(\" moonshot \") = %v, want moonshot", got)
	}

	if got := normalizeHeaders(nil); got != nil {
		t.Errorf("normalizeHeaders(nil) = %v, want nil", got)
	}
	if got := normalizeHeaders(map[string]string{}); got != nil {
		t.Errorf("normalizeHeaders({}) = %v, want nil", got)
	}
	kept := map[string]string{"X-Api-Version": "2024-06-01"}
	if got := normalizeHeaders(kept); len(got) != 1 || got["X-Api-Version"] != "2024-06-01" {
		t.Errorf("normalizeHeaders(%v) = %v, want it unchanged", kept, got)
	}
}

// TestValidateProviderVendorChecksTheColumnLength covers the label limit.
func TestValidateProviderVendorChecksTheColumnLength(t *testing.T) {
	if err := validateProviderVendor(""); err != nil {
		t.Errorf("validateProviderVendor(\"\") = %v, want nil", err)
	}
	if err := validateProviderVendor(strings.Repeat("v", maxProviderVendorLen)); err != nil {
		t.Errorf("validateProviderVendor at the limit = %v, want nil", err)
	}
	if err := validateProviderVendor(strings.Repeat("v", maxProviderVendorLen+1)); err == nil {
		t.Error("validateProviderVendor over the limit accepted the value, want an error")
	}
}

// TestRenderProviderHeaderValueFillsKnownPlaceholders covers the template a
// stored header may carry: a known ${name} is replaced, text around it stays,
// and a value with no placeholder is returned unchanged.
func TestRenderProviderHeaderValueFillsKnownPlaceholders(t *testing.T) {
	vars := map[string]string{"session_id": "s-1", "provider_id": "p-1"}

	got, ok := RenderProviderHeaderValue("${session_id}", vars)
	if !ok || got != "s-1" {
		t.Errorf("render = %q, %v, want s-1, true", got, ok)
	}

	got, ok = RenderProviderHeaderValue("rqark:${provider_id}/${session_id}", vars)
	if !ok || got != "rqark:p-1/s-1" {
		t.Errorf("render = %q, %v, want rqark:p-1/s-1, true", got, ok)
	}

	got, ok = RenderProviderHeaderValue("plain", vars)
	if !ok || got != "plain" {
		t.Errorf("render = %q, %v, want plain, true", got, ok)
	}
}

// TestRenderProviderHeaderValueDropsAHeaderWithAMissingValue covers the call
// that has nothing to fill a placeholder with: the header is dropped rather
// than sent half-filled.
func TestRenderProviderHeaderValueDropsAHeaderWithAMissingValue(t *testing.T) {
	if got, ok := RenderProviderHeaderValue("${session_id}", map[string]string{}); ok {
		t.Errorf("render = %q, %v, want the header dropped", got, ok)
	}
	if _, ok := RenderProviderHeaderValue("${session_id}", map[string]string{"session_id": ""}); ok {
		t.Error("an empty variable filled the placeholder, want the header dropped")
	}
}

// TestValidateProviderHeadersChecksPlaceholders covers the write-time check: a
// placeholder the proxy cannot fill, and one that is never closed, are refused
// rather than sent upstream as literal text.
func TestValidateProviderHeadersChecksPlaceholders(t *testing.T) {
	accepted := map[string]string{
		"x-opencode-session": "${session_id}",
		"X-Api-Version":      "2024-06-01",
		"X-Mixed":            "rqark/${agent_id}",
	}
	if err := validateProviderHeaders(accepted); err != nil {
		t.Errorf("validateProviderHeaders(%v) = %v, want nil", accepted, err)
	}

	for name, headers := range map[string]map[string]string{
		"unknown variable":     {"X-Api": "${nope}"},
		"empty variable name":  {"X-Api": "${}"},
		"unclosed placeholder": {"X-Api": "${session_id"},
	} {
		err := validateProviderHeaders(headers)
		if err == nil {
			t.Errorf("%s: validateProviderHeaders accepted the value, want an error", name)
			continue
		}
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Errorf("%s: error = %v, want a ValidationError", name, err)
		}
	}
}
