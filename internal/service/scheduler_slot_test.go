package service

import (
	"testing"
	"time"
)

// TestNextEverySlotLandsOnTheMinute covers the reason the grid exists: a task
// that repeats every minute fires on every minute, not one minute after whatever
// instant it happened to be claimed at.
func TestNextEverySlotLandsOnTheMinute(t *testing.T) {
	now := time.Date(2026, 10, 17, 10, 23, 37, 912_000_000, time.UTC)

	next := NextEverySlot(now, time.Minute)

	want := time.Date(2026, 10, 17, 10, 24, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextEverySlot = %s, want %s", next, want)
	}
}

func TestNextEverySlotLandsOnTheHour(t *testing.T) {
	now := time.Date(2026, 10, 17, 10, 23, 37, 0, time.UTC)

	next := NextEverySlot(now, time.Hour)

	want := time.Date(2026, 10, 17, 11, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextEverySlot = %s, want %s", next, want)
	}
}

func TestNextEverySlotLandsOnTheDay(t *testing.T) {
	now := time.Date(2026, 10, 17, 10, 23, 37, 0, time.UTC)

	next := NextEverySlot(now, 24*time.Hour)

	want := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextEverySlot = %s, want %s", next, want)
	}
}

// TestNextEverySlotIsStrictlyAfter covers the boundary: an instant already on the
// grid moves to the next one, so a call straight after a fire cannot return the
// slot that just ran.
func TestNextEverySlotIsStrictlyAfter(t *testing.T) {
	onTheGrid := time.Date(2026, 10, 17, 10, 24, 0, 0, time.UTC)

	next := NextEverySlot(onTheGrid, time.Minute)

	want := time.Date(2026, 10, 17, 10, 25, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextEverySlot = %s, want %s", next, want)
	}
}

// TestNextEverySlotSkipsAMissedBacklog covers a run that overran several slots:
// the next instant is the first one still ahead, so missed turns are skipped
// rather than run back to back.
func TestNextEverySlotSkipsAMissedBacklog(t *testing.T) {
	// Five minutes after the slot it should have been claimed at.
	now := time.Date(2026, 10, 17, 10, 29, 12, 0, time.UTC)

	next := NextEverySlot(now, time.Minute)

	want := time.Date(2026, 10, 17, 10, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextEverySlot = %s, want %s", next, want)
	}
}

// TestNextEverySlotDoesNotDrift covers the property the old `now + gap` rule
// lacked: repeated calls from instants a second or two apart keep landing on the
// same grid.
func TestNextEverySlotDoesNotDrift(t *testing.T) {
	base := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

	for offset := 0; offset < 60; offset++ {
		now := base.Add(time.Duration(offset)*time.Second + 137*time.Millisecond)
		next := NextEverySlot(now, time.Minute)

		if next.Second() != 0 || next.Nanosecond() != 0 {
			t.Fatalf("offset %ds: next = %s, want a whole minute", offset, next)
		}
	}
}

// TestNextEverySlotHandlesAnAwkwardGap covers a gap that divides neither an hour
// nor a day: the grid is still the epoch's, so the instants stay regular.
func TestNextEverySlotHandlesAnAwkwardGap(t *testing.T) {
	now := time.Date(2026, 10, 17, 10, 23, 37, 0, time.UTC)

	next := NextEverySlot(now, 90*time.Second)

	want := time.Date(2026, 10, 17, 10, 24, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextEverySlot = %s, want %s", next, want)
	}
}

// TestATimezoneResolvesInTheServerImage covers the failure a host-only test
// cannot see: the server runs on alpine, which ships no zoneinfo, so a zone name
// only resolves because the database is compiled into the binary. A test that
// runs on a developer machine passes either way; this one at least pins the
// behaviour the container depends on.
func TestATimezoneResolvesInTheServerImage(t *testing.T) {
	for _, name := range []string{"Asia/Hong_Kong", "America/New_York", "Europe/London", "UTC"} {
		location, err := LoadTaskLocation(name)
		if err != nil {
			t.Errorf("LoadTaskLocation(%q) = %v, want a location", name, err)
			continue
		}
		if location.String() != name {
			t.Errorf("LoadTaskLocation(%q) = %q", name, location)
		}
	}
}
