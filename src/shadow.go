package main

// shadow.go -- the NETMIDI tab on Push's screen. push-manager polls the
// view and posts every press back; the hack owns the cursor and what a
// button does. Everything here is ASCII: the on-device font has nothing
// else.

import (
	"fmt"
	"strings"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/push3"
)

// offArmFor is how long the first press of OFF waits for the second.
const offArmFor = 4 * time.Second

// shadowView is what push-manager draws (its README, "Shadow UI tabs from
// other hacks").
type shadowView struct {
	Title   string   `json:"title"`
	Status  string   `json:"status,omitempty"`
	Rows    []string `json:"rows"`
	Cursor  int      `json:"cursor"`
	Buttons []string `json:"buttons"`
	Hint    string   `json:"hint"`
}

type rowKind int

const (
	rowSession rowKind = iota
	rowPeer
	rowFound
	rowSaved
	rowRoute
	rowNote
)

type shadowRow struct {
	kind rowKind
	idx  int
	text string
}

func ascii(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r > 0x7E {
			r = '?'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (a *App) shadowRows(s State) []shadowRow {
	var rows []shadowRow
	sess := "SESSION  OFF"
	if s.Enabled {
		sess = fmt.Sprintf("SESSION  ON   %s  UDP %d", ascii(s.Name), s.Port)
		if s.Announced == "" {
			sess += "  (not announced)"
		}
	}
	rows = append(rows, shadowRow{rowSession, 0, sess})
	for i, p := range s.Peers {
		state := "connecting"
		if p.State == "connected" {
			state = fmt.Sprintf("%.1f ms", p.LatencyMs)
		}
		rows = append(rows, shadowRow{rowPeer, i, fmt.Sprintf("PEER     %-18s %-21s %s", ascii(p.Name), p.Addr, state)})
	}
	for i, f := range s.Found {
		if f.Own {
			continue
		}
		rows = append(rows, shadowRow{rowFound, i, fmt.Sprintf("FOUND    %-18s %s:%d", ascii(f.Name), f.Addr, f.Port)})
	}
	for i, r := range s.Remembered {
		rows = append(rows, shadowRow{rowSaved, i, fmt.Sprintf("SAVED    %-18s %s:%d", ascii(r.Name), r.Host, r.Port)})
	}
	for i, r := range s.Routes {
		on, there := "off", ""
		if r.On {
			on = "on"
		}
		if !r.Resolved {
			there = "  (port not found)"
		}
		ch := "ch as is"
		if r.Channel > 0 {
			ch = fmt.Sprintf("ch %d", r.Channel)
		}
		rows = append(rows, shadowRow{rowRoute, i, fmt.Sprintf("ROUTE %-3s %-24s %-8s %s%s", r.Dir, ascii(r.Port), ch, on, there)})
	}
	if len(s.Peers) == 0 && s.Enabled {
		rows = append(rows, shadowRow{rowNote, 0, "no peer yet: on the iPad, open its network session and tap " + ascii(s.Name)})
	}
	return rows
}

func (a *App) shadowView() shadowView {
	s := a.State()
	rows := a.shadowRows(s)
	a.mu.Lock()
	if a.cursor >= len(rows) {
		a.cursor = len(rows) - 1
	}
	if a.cursor < 0 {
		a.cursor = 0
	}
	cur := a.cursor
	a.mu.Unlock()
	v := shadowView{Title: fmt.Sprintf("NET MIDI  %d connected", connectedCount(s)), Cursor: cur,
		Buttons: make([]string, 8), Hint: "jog: move"}
	if s.Error != "" {
		v.Status = ascii(s.Error)
	} else if !s.Alsa {
		v.Status = "waiting for MIDI (boot settle)"
	}
	for _, r := range rows {
		v.Rows = append(v.Rows, r.text)
	}
	a.mu.Lock()
	armed := time.Since(a.offArmed) < offArmFor
	a.mu.Unlock()
	switch {
	case !s.Enabled:
		v.Buttons[0] = "ON"
	case armed:
		v.Buttons[0] = "OFF: SURE?"
		if v.Status == "" {
			v.Status = "press again to switch the session off and drop every peer"
		}
	default:
		v.Buttons[0] = "OFF"
	}
	if len(rows) > 0 {
		switch r := rows[cur]; r.kind {
		case rowPeer:
			v.Buttons[2] = "DROP"
		case rowFound:
			v.Buttons[1] = "CONNECT"
		case rowSaved:
			v.Buttons[1] = "CONNECT"
			v.Buttons[2] = "FORGET"
		case rowRoute:
			if s.Routes[r.idx].On {
				v.Buttons[3] = "ROUTE OFF"
			} else {
				v.Buttons[3] = "ROUTE ON"
			}
		}
	}
	return v
}

func connectedCount(s State) int {
	n := 0
	for _, p := range s.Peers {
		if p.State == "connected" {
			n++
		}
	}
	return n
}

// shadowPress acts on one control from push-manager.
func (a *App) shadowPress(cc, value int) {
	if value == 0 && cc != push3.CCJogWheel {
		return // releases
	}
	s := a.State()
	rows := a.shadowRows(s)
	a.mu.Lock()
	cur := a.cursor
	a.mu.Unlock()
	if cur >= len(rows) {
		cur = len(rows) - 1
	}
	switch cc {
	case push3.CCJogWheel:
		if value == 1 {
			cur++
		} else if value == 127 {
			cur--
		}
		if cur >= len(rows) {
			cur = len(rows) - 1
		}
		if cur < 0 {
			cur = 0
		}
		a.mu.Lock()
		a.cursor = cur
		a.mu.Unlock()
	case push3.CCScreenBot1:
		// OFF drops every peer, so it takes two presses: the first arms it
		// for a few seconds, the second does it.
		a.mu.Lock()
		armed := time.Since(a.offArmed) < offArmFor
		a.offArmed = time.Time{}
		if s.Enabled && !armed {
			a.offArmed = time.Now()
		}
		a.mu.Unlock()
		if !s.Enabled || armed {
			on := !s.Enabled
			a.SetSession(nil, nil, nil, &on)
		}
	case push3.CCScreenBot2:
		if cur < 0 {
			return
		}
		switch r := rows[cur]; r.kind {
		case rowFound:
			f := s.Found[r.idx]
			a.Connect(f.Addr, f.Port, false, f.Name)
		case rowSaved:
			r := s.Remembered[r.idx]
			a.Connect(r.Host, r.Port, false, r.Name)
		}
	case push3.CCScreenBot3:
		if cur < 0 {
			return
		}
		switch r := rows[cur]; r.kind {
		case rowPeer:
			a.Disconnect(s.Peers[r.idx].SSRC)
		case rowSaved:
			a.Forget(s.Remembered[r.idx].Host, s.Remembered[r.idx].Port)
		}
	case push3.CCScreenBot4:
		if cur < 0 || rows[cur].kind != rowRoute {
			return
		}
		rs := make([]Route, len(s.Routes))
		for i, r := range s.Routes {
			rs[i] = r.Route
		}
		rs[rows[cur].idx].On = !rs[rows[cur].idx].On
		a.SetRoutes(rs)
	}
	a.touch()
}
