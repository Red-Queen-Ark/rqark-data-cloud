package config

import (
	"testing"
	"time"
)

// TestGetEnvDuration covers the reader the task settings use: a whole number of
// seconds, with an empty, non-numeric or negative value falling back. An unset
// variable reads exactly like an empty one, since that is what GetEnv does.
func TestGetEnvDuration(t *testing.T) {
	fallback := 300 * time.Second

	cases := map[string]time.Duration{
		"":     fallback,
		"600":  600 * time.Second,
		"0":    0,
		"ten":  fallback,
		"-5":   fallback,
		"1.5":  fallback,
		" 600": fallback,
		"3600": time.Hour,
	}

	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			const key = "TEST_GET_ENV_DURATION"
			t.Setenv(key, value)

			if got := GetEnvDuration(key, fallback); got != want {
				t.Errorf("GetEnvDuration(%q) = %s, want %s", value, got, want)
			}
		})
	}
}

// TestGetEnvBool covers the switch that gates self-registration: only a value
// strconv.ParseBool accepts counts, everything else falls back.
func TestGetEnvBool(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"true":  true,
		"1":     true,
		"TRUE":  true,
		"false": false,
		"0":     false,
		"yes":   false,
		" ":     false,
	}

	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			const key = "TEST_GET_ENV_BOOL"
			t.Setenv(key, value)

			if got := GetEnvBool(key, false); got != want {
				t.Errorf("GetEnvBool(%q) = %v, want %v", value, got, want)
			}
		})
	}
}
