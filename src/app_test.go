package main

import (
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/rgeissen/push-hack-netmidi/rtpmidi"
)

// Switching the session off with a peer connected says bye to it, and the
// disconnect event comes straight back into the app: that must not wait
// on the lock SetSession holds (it did, 2026-10-26, and froze the hack).
func TestSessionOffWithPeerDoesNotDeadlock(t *testing.T) {
	a := newApp(filepath.Join(t.TempDir(), "config.json"), "test")
	a.cfg.Port = 15020
	a.mu.Lock()
	a.up()
	a.mu.Unlock()
	if a.tr == nil {
		t.Skip("cannot bind UDP 15020")
	}
	defer a.Stop()

	peer, err := rtpmidi.Listen(15030, "iPad")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.Invite(netip.MustParseAddrPort("127.0.0.1:15020"))
	deadline := time.Now().Add(5 * time.Second)
	for a.tr.Connected() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the peer never connected")
		}
		time.Sleep(20 * time.Millisecond)
	}

	done := make(chan error, 1)
	off := false
	go func() { done <- a.SetSession(nil, nil, nil, nil, &off) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SetSession(off) hangs")
	}
	if s := a.State(); s.Enabled || len(s.Peers) != 0 {
		t.Fatalf("after off: %+v", s)
	}
	on := true
	if err := a.SetSession(nil, nil, nil, nil, &on); err != nil {
		t.Fatal(err)
	}
	if s := a.State(); !s.Enabled || a.tr == nil {
		t.Fatalf("after on: %+v", s)
	}
}
