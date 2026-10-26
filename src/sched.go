package main

// sched.go -- delivers messages from the network when they are due.
//
// A packet carries the sender's time and the delta times between its
// messages. Delivering everything the moment the packet lands throws that
// away: a short note whose on and off share a packet gets no length, and
// Wi-Fi's jitter lands on every note. So each message is held until its
// time, plus a small buffer that absorbs the jitter.

import (
	"container/heap"
	"sync"
	"time"
)

type timed struct {
	at  time.Time
	msg []byte
	n   uint64 // arrival order, for messages due at the same time
}

type timedHeap []timed

func (h timedHeap) Len() int { return len(h) }
func (h timedHeap) Less(i, j int) bool {
	if h[i].at.Equal(h[j].at) {
		return h[i].n < h[j].n
	}
	return h[i].at.Before(h[j].at)
}
func (h timedHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *timedHeap) Push(x any)         { *h = append(*h, x.(timed)) }
func (h *timedHeap) Pop() any           { old := *h; x := old[len(old)-1]; *h = old[:len(old)-1]; return x }
func (h timedHeap) peek() (timed, bool) { return h[0], len(h) > 0 }

type sched struct {
	deliver func(msg []byte)
	mu      sync.Mutex
	q       timedHeap
	n       uint64
	wake    chan struct{}
	done    chan struct{}
}

func newSched(deliver func(msg []byte)) *sched {
	s := &sched{deliver: deliver, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go s.run()
	return s
}

// add queues a message for a time. One in the past goes out at once.
func (s *sched) add(at time.Time, msg []byte) {
	s.mu.Lock()
	s.n++
	heap.Push(&s.q, timed{at: at, msg: msg, n: s.n})
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *sched) run() {
	for {
		s.mu.Lock()
		wait := time.Hour
		if len(s.q) > 0 {
			d := time.Until(s.q[0].at)
			if d <= 0 {
				t := heap.Pop(&s.q).(timed)
				s.mu.Unlock()
				s.deliver(t.msg)
				continue
			}
			wait = d
		}
		s.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		case <-s.done:
			timer.Stop()
			return
		}
	}
}

// pending counts the messages waiting.
func (s *sched) pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.q)
}

func (s *sched) close() { close(s.done) }
