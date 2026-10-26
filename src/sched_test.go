package main

import (
	"sync"
	"testing"
	"time"
)

// Messages go out at their time, in time order, and the spacing between
// them survives.
func TestSchedKeepsOrderAndSpacing(t *testing.T) {
	var mu sync.Mutex
	var got []byte
	var at []time.Time
	s := newSched(func(msg []byte) {
		mu.Lock()
		got = append(got, msg[0])
		at = append(at, time.Now())
		mu.Unlock()
	})
	defer s.close()
	now := time.Now()
	s.add(now.Add(60*time.Millisecond), []byte{3})
	s.add(now.Add(20*time.Millisecond), []byte{1})
	s.add(now.Add(40*time.Millisecond), []byte{2})
	s.add(now.Add(-time.Second), []byte{0}) // overdue: at once
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if string(got) != "\x00\x01\x02\x03" {
		t.Fatalf("order %v", got)
	}
	if d := at[3].Sub(at[1]); d < 35*time.Millisecond || d > 70*time.Millisecond {
		t.Errorf("3 came %v after 1, want about 40ms", d)
	}
	if s.pending() != 0 {
		t.Errorf("%d still pending", s.pending())
	}
}
