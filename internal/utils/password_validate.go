package utils

import (
	"regexp"
	"strings"
	"unicode"
)

// MinPasswordLength is the minimum length enforced by the length validator.
const MinPasswordLength = 8

// maxSimilarity mirrors Django's UserAttributeSimilarityValidator default.
const maxSimilarity = 0.7

var nonWord = regexp.MustCompile(`\W+`)

// Violation is a single password policy failure.
type Violation struct {
	Code    string `json:"code" example:"password_too_short"`
	Message string `json:"message" example:"This password is too short. It must contain at least 8 characters."`
}

// Attributes are the user fields the similarity validator compares against.
type Attributes struct {
	Username    string
	Email       string
	DisplayName string
}

// ValidatePassword returns every policy violation for password, in the order the
// validators run in Django. An empty slice means the password is acceptable.
func ValidatePassword(pw string, attrs Attributes) []Violation {
	var violations []Violation

	if v, ok := validateSimilarity(pw, attrs); ok {
		violations = append(violations, v)
	}
	if v, ok := validateMinLength(pw); ok {
		violations = append(violations, v)
	}
	if v, ok := validateNumeric(pw); ok {
		violations = append(violations, v)
	}

	return violations
}

// validateSimilarity ports UserAttributeSimilarityValidator: the password may
// not be too similar to the username, email or display name, comparing both the
// whole attribute and its non-word separated parts.
func validateSimilarity(pw string, attrs Attributes) (Violation, bool) {
	lower := strings.ToLower(pw)

	for _, attr := range []struct {
		name  string
		value string
	}{
		{"username", attrs.Username},
		{"email", attrs.Email},
		{"display_name", attrs.DisplayName},
	} {
		if attr.value == "" {
			continue
		}

		valueLower := strings.ToLower(attr.value)
		parts := append(nonWord.Split(valueLower, -1), valueLower)
		for _, part := range parts {
			if part == "" {
				continue
			}
			if exceedsMaximumLengthRatio([]rune(lower), maxSimilarity, []rune(part)) {
				continue
			}
			if quickRatio([]rune(lower), []rune(part)) >= maxSimilarity {
				return Violation{
					Code:    "password_too_similar",
					Message: "The password is too similar to the " + attr.name + ".",
				}, true
			}
		}
	}

	return Violation{}, false
}

// validateMinLength ports MinimumLengthValidator.
func validateMinLength(pw string) (Violation, bool) {
	if len([]rune(pw)) < MinPasswordLength {
		return Violation{
			Code:    "password_too_short",
			Message: "This password is too short. It must contain at least 8 characters.",
		}, true
	}
	return Violation{}, false
}

// validateNumeric ports NumericPasswordValidator.
func validateNumeric(pw string) (Violation, bool) {
	if pw == "" {
		return Violation{}, false
	}
	for _, r := range pw {
		if !unicode.IsDigit(r) {
			return Violation{}, false
		}
	}
	return Violation{
		Code:    "password_entirely_numeric",
		Message: "This password is entirely numeric.",
	}, true
}
