package rtpmidi

import (
	"testing"
	"time"
)

// A clock sync with a long round trip does not move the offset a short
// one gave: the long one waited somewhere on the way.
func TestOffsetKeepsTheShortestRoundTrip(t *testing.T) {
	var p Peer
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	p.adopt(100, 500, now)
	p.adopt(900, 900, now.Add(3*time.Second)) // slow, and 40 ms off
	if p.Offset != 500 {
		t.Errorf("offset %d, want 500", p.Offset)
	}
	if p.Latency != 45*time.Millisecond {
		t.Errorf("latency %v shows the latest trip, want 45ms", p.Latency)
	}
	p.adopt(100, 520, now.Add(6*time.Second)) // as short, newer: wins
	if p.Offset != 520 {
		t.Errorf("offset %d, want 520", p.Offset)
	}
	// The short one is forgotten after syncFor; the slow one then counts.
	p.adopt(900, 900, now.Add(syncFor+10*time.Second))
	if p.Offset != 900 {
		t.Errorf("offset %d after the short samples aged out, want 900", p.Offset)
	}
}
