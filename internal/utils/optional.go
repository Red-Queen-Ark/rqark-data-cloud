package utils

// Optional is a partial-update field that can be absent, null or set. A plain
// pointer cannot tell the first two apart, yet for a nullable column they mean
// different things: an absent key leaves the stored value alone, an explicit
// null clears it. The decoder fills Set from the keys the body actually
// carried.
type Optional[T any] struct {
	// Set reports whether the request carried the field at all.
	Set bool
	// Value is the decoded value, or nil for an explicit null.
	Value *T
}
