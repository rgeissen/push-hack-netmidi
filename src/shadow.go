package main

// shadow.go -- the NETMIDI tab on Push's screen. push-manager polls the
// view and posts every press back; the hack owns the cursor and what a
// button does. Everything here is ASCII: the on-device font has nothing
// else.
//
// The tab has every setting of the web page but the ones that need typing
// (the session's name and port, an address to connect to). Two views: the
// main list, and a port picker for a new route.

import (
	"fmt"
	"strings"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/push3"
)

// armFor is how long the first press of a destructive button (OFF,
// REMOVE) waits for the second.
const armFor = 4 * time.Second

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
	rowClock
	rowBuffer
	rowPeer
	rowFound
	rowSaved
	rowRoute
	rowAddRoute
	rowNote
)

type shadowRow struct {
	kind rowKind
	idx  int
	text string
}

// shadowState is the tab's own state: the cursor of each view, which view
// is open, the ports the picker shows, and what is armed.
type shadowState struct {
	cursor   int
	picking  bool // the port picker is open
	pickCur  int
	ports    []portView
	offArmed time.Time
	rmArmed  time.Time
	rmRoute  int
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
	clock := "CLOCK IN off  (the Push stays the sync master)"
	if s.ClockIn {
		clock = "CLOCK IN on   (clock and transport from the network reach Live)"
	}
	rows = append(rows, shadowRow{rowClock, 0, clock})
	buf := fmt.Sprintf("BUFFER   %d ms", s.LatencyMs)
	if s.BehindMs > 0 {
		buf += fmt.Sprintf("   (packets lately up to %.0f ms behind their time)", s.BehindMs)
	}
	rows = append(rows, shadowRow{rowBuffer, 0, buf})
	for i, p := range s.Peers {
		state := "connecting"
		if p.State == "connected" {
			state = fmt.Sprintf("%.1f ms  lost %d", p.LatencyMs, p.Lost)
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
	rows = append(rows, shadowRow{rowAddRoute, 0, "ADD ROUTE ...  (another port to or from the network)"})
	if len(s.Peers) == 0 && s.Enabled {
		rows = append(rows, shadowRow{rowNote, 0, "no peer yet: on the iPad, open its network session and tap " + ascii(s.Name)})
	}
	return rows
}

func clamp(i, n int) int {
	if i >= n {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	return i
}

func (a *App) shadowView() shadowView {
	s := a.State()
	a.mu.Lock()
	sh := a.shadow
	a.mu.Unlock()
	if sh.picking {
		return a.pickerView(sh)
	}
	rows := a.shadowRows(s)
	cur := clamp(sh.cursor, len(rows))
	a.mu.Lock()
	a.shadow.cursor = cur
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
	now := time.Now()
	switch {
	case !s.Enabled:
		v.Buttons[0] = "ON"
	case now.Sub(sh.offArmed) < armFor:
		v.Buttons[0] = "OFF: SURE?"
		if v.Status == "" {
			v.Status = "press again to switch the session off and drop every peer"
		}
	default:
		v.Buttons[0] = "OFF"
	}
	if len(rows) > 0 {
		switch r := rows[cur]; r.kind {
		case rowClock:
			if s.ClockIn {
				v.Buttons[3] = "CLOCK OFF"
			} else {
				v.Buttons[3] = "CLOCK ON"
			}
		case rowBuffer:
			v.Buttons[4], v.Buttons[5], v.Buttons[6], v.Buttons[7] = "-5 MS", "+5 MS", "-50 MS", "+50 MS"
		case rowPeer:
			v.Buttons[2] = "DROP"
		case rowFound:
			v.Buttons[1] = "CONNECT"
			v.Buttons[2] = "CONNECT+SAVE"
		case rowSaved:
			v.Buttons[1] = "CONNECT"
			v.Buttons[2] = "FORGET"
		case rowRoute:
			if s.Routes[r.idx].On {
				v.Buttons[3] = "ROUTE OFF"
			} else {
				v.Buttons[3] = "ROUTE ON"
			}
			v.Buttons[4], v.Buttons[5] = "CH -", "CH +"
			if now.Sub(sh.rmArmed) < armFor && sh.rmRoute == r.idx {
				v.Buttons[6] = "REMOVE: SURE?"
				if v.Status == "" {
					v.Status = "press again to remove the route"
				}
			} else {
				v.Buttons[6] = "REMOVE"
			}
		case rowAddRoute:
			v.Buttons[1] = "ADD"
		}
	}
	return v
}

// pickerView lists the ALSA ports a new route could use.
func (a *App) pickerView(sh shadowState) shadowView {
	v := shadowView{Title: "NET MIDI  new route: pick a port", Buttons: make([]string, 8), Hint: "jog: move"}
	v.Buttons[0] = "BACK"
	if len(sh.ports) == 0 {
		v.Rows = []string{"no ports (the MIDI side is not up yet)"}
		return v
	}
	for _, p := range sh.ports {
		v.Rows = append(v.Rows, fmt.Sprintf("%-28s %-24s %s", ascii(p.Client), ascii(p.Port), p.Caps))
	}
	v.Cursor = clamp(sh.pickCur, len(sh.ports))
	p := sh.ports[v.Cursor]
	if strings.Contains(p.Caps, "R") {
		v.Buttons[1] = "TO NET"
	}
	if strings.Contains(p.Caps, "W") {
		v.Buttons[2] = "FROM NET"
	}
	v.Status = "R: the port sends (a route to the network reads it); W: it receives"
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
	a.mu.Lock()
	picking := a.shadow.picking
	a.mu.Unlock()
	if picking {
		a.pickerPress(cc, value)
		return
	}
	s := a.State()
	rows := a.shadowRows(s)
	a.mu.Lock()
	cur := clamp(a.shadow.cursor, len(rows))
	a.mu.Unlock()
	routes := func() []Route {
		rs := make([]Route, len(s.Routes))
		for i, r := range s.Routes {
			rs[i] = r.Route
		}
		return rs
	}
	switch cc {
	case push3.CCJogWheel:
		if value == 1 {
			cur++
		} else if value == 127 {
			cur--
		}
		a.mu.Lock()
		a.shadow.cursor = clamp(cur, len(rows))
		a.mu.Unlock()
	case push3.CCScreenBot1:
		// OFF drops every peer, so it takes two presses: the first arms it
		// for a few seconds, the second does it.
		a.mu.Lock()
		armed := time.Since(a.shadow.offArmed) < armFor
		a.shadow.offArmed = time.Time{}
		if s.Enabled && !armed {
			a.shadow.offArmed = time.Now()
		}
		a.mu.Unlock()
		if !s.Enabled || armed {
			on := !s.Enabled
			a.SetSession(nil, nil, nil, nil, &on)
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
		case rowAddRoute:
			ports := a.Ports()
			a.mu.Lock()
			a.shadow.picking, a.shadow.pickCur, a.shadow.ports = true, 0, ports
			a.mu.Unlock()
		}
	case push3.CCScreenBot3:
		if cur < 0 {
			return
		}
		switch r := rows[cur]; r.kind {
		case rowPeer:
			a.Disconnect(s.Peers[r.idx].SSRC)
		case rowFound:
			f := s.Found[r.idx]
			a.Connect(f.Addr, f.Port, true, f.Name)
		case rowSaved:
			a.Forget(s.Remembered[r.idx].Host, s.Remembered[r.idx].Port)
		}
	case push3.CCScreenBot4:
		if cur < 0 {
			return
		}
		switch r := rows[cur]; r.kind {
		case rowClock:
			in := !s.ClockIn
			a.SetSession(nil, nil, nil, &in, nil)
		case rowRoute:
			rs := routes()
			rs[r.idx].On = !rs[r.idx].On
			a.SetRoutes(rs)
		}
	case push3.CCScreenBot5, push3.CCScreenBot6, push3.CCScreenBot7, push3.CCScreenBot8:
		if cur < 0 {
			return
		}
		switch r := rows[cur]; r.kind {
		case rowBuffer:
			step := map[int]int{push3.CCScreenBot5: -5, push3.CCScreenBot6: 5, push3.CCScreenBot7: -50, push3.CCScreenBot8: 50}[cc]
			ms := max(0, min(500, s.LatencyMs+step))
			a.SetSession(nil, nil, &ms, nil, nil)
		case rowRoute:
			switch cc {
			case push3.CCScreenBot5, push3.CCScreenBot6:
				rs := routes()
				ch := rs[r.idx].Channel - 1
				if cc == push3.CCScreenBot6 {
					ch = rs[r.idx].Channel + 1
				}
				rs[r.idx].Channel = max(0, min(16, ch))
				a.SetRoutes(rs)
			case push3.CCScreenBot7:
				a.mu.Lock()
				armed := time.Since(a.shadow.rmArmed) < armFor && a.shadow.rmRoute == r.idx
				a.shadow.rmArmed, a.shadow.rmRoute = time.Time{}, -1
				if !armed {
					a.shadow.rmArmed, a.shadow.rmRoute = time.Now(), r.idx
				}
				a.mu.Unlock()
				if armed {
					rs := routes()
					a.SetRoutes(append(rs[:r.idx], rs[r.idx+1:]...))
				}
			}
		}
	}
	a.touch()
}

// pickerPress acts on a press while the port picker is open.
func (a *App) pickerPress(cc, value int) {
	a.mu.Lock()
	sh := a.shadow
	a.mu.Unlock()
	switch cc {
	case push3.CCJogWheel:
		cur := sh.pickCur
		if value == 1 {
			cur++
		} else if value == 127 {
			cur--
		}
		a.mu.Lock()
		a.shadow.pickCur = clamp(cur, len(sh.ports))
		a.mu.Unlock()
	case push3.CCScreenBot1:
		a.mu.Lock()
		a.shadow.picking = false
		a.mu.Unlock()
	case push3.CCScreenBot2, push3.CCScreenBot3:
		if len(sh.ports) == 0 {
			return
		}
		p := sh.ports[clamp(sh.pickCur, len(sh.ports))]
		dir := "out"
		if cc == push3.CCScreenBot3 {
			dir = "in"
		}
		if dir == "out" && !strings.Contains(p.Caps, "R") || dir == "in" && !strings.Contains(p.Caps, "W") {
			return
		}
		s := a.State()
		rs := make([]Route, len(s.Routes))
		for i, r := range s.Routes {
			rs[i] = r.Route
		}
		rs = append(rs, Route{Client: p.Client, Port: p.Port, Dir: dir, On: true})
		a.SetRoutes(rs)
		a.mu.Lock()
		a.shadow.picking = false
		a.mu.Unlock()
	}
	a.touch()
}
