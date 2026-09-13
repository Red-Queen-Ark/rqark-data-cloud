package utils

// quickRatio ports SequenceMatcher.quick_ratio from CPython's difflib. It
// counts the characters of a that are available in b and returns twice that
// count divided by the combined length, an upper bound of the real similarity
// ratio.
func quickRatio(a, b []rune) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}

	fullBCount := make(map[rune]int, len(b))
	for _, r := range b {
		fullBCount[r]++
	}

	avail := make(map[rune]int)
	matches := 0
	for _, r := range a {
		if n := avail[r]; n > 0 {
			avail[r] = n - 1
			matches++
			continue
		}
		if n := fullBCount[r]; n > 0 {
			avail[r] = n - 1
			matches++
		}
	}

	return 2 * float64(matches) / float64(len(a)+len(b))
}

// exceedsMaximumLengthRatio ports Django's helper of the same name. A value that
// is far shorter than the password cannot reach maxSimilarity, so the expensive
// ratio computation is skipped.
func exceedsMaximumLengthRatio(password []rune, maxSimilarity float64, value []rune) bool {
	passwordLen := len(password)
	lengthBoundSimilarity := maxSimilarity / 2 * float64(passwordLen)
	valueLen := len(value)
	return passwordLen >= 10*valueLen && float64(valueLen) < lengthBoundSimilarity
}
