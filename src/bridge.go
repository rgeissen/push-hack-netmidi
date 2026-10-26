package main

// bridge.go -- ALSA on one side, the network on the other.
//
// The hack owns one port, "Net MIDI", that reads and writes: Live lists it
// as an input and an output. What Live (or anyone) writes to it goes to the
// network; what comes from the network goes to everyone connected to it.
// Routes add other ports: an "out" route subscribes the hack to a port
// (Stacks Out, say) and sends what it plays to the network; an "in" route
// sends what the network plays to a port (Riffs In). A route's port may be
// absent (its hack stopped): the bridge looks for it again every few
// seconds and subscribes when it is back.

import (
	"errors"
	"log"
	"sync"
	"syscall"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/alsaseq"
)

const (
	clientName  = "Push Hack Net MIDI"
	ownPortName = "Net MIDI"
	resolveEach = 3 * time.Second
)

type liveRoute struct {
	Route
	addr     alsaseq.Addr
	resolved bool
}

// Bridge is the ALSA side.
type Bridge struct {
	ToNet   func(msgs [][]byte)                    // what leaves for the network
	Monitor func(dir, via string, msg []byte)      // every message, for the page
	OnPorts func(routes []liveRoute, changed bool) // a route got or lost its port

	c      *alsaseq.Client
	mu     sync.Mutex
	routes []*liveRoute
	bySrc  map[alsaseq.Addr]*liveRoute
	done   chan struct{}
}

// openBridge opens the hack's port. Call it after alsaseq.WaitForBootSettle.
func openBridge() (*Bridge, error) {
	c, err := alsaseq.Open()
	if err != nil {
		return nil, err
	}
	if err := c.SetName(clientName); err != nil {
		log.Printf("alsa: %v (client keeps its default name)", err)
	}
	caps := alsaseq.CapRead | alsaseq.CapSubsRead | alsaseq.CapWrite | alsaseq.CapSubsWrite
	if _, err := c.CreatePort(ownPortName, caps, alsaseq.PortTypeMidi|alsaseq.PortTypeApp); err != nil {
		c.Close()
		return nil, err
	}
	return &Bridge{c: c, bySrc: map[alsaseq.Addr]*liveRoute{}, done: make(chan struct{})}, nil
}

// Addr is the hack's own port.
func (b *Bridge) Addr() alsaseq.Addr { return b.c.Addr() }

// Run reads the port until Close. A read's events go to the network in one
// packet, so a chord stays a chord.
func (b *Bridge) Run() {
	go b.resolveLoop()
	buf := make([]byte, 8192)
	for {
		n, err := syscall.Read(b.c.FD(), buf)
		if err != nil {
			select {
			case <-b.done:
				return
			default:
			}
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				continue
			}
			log.Printf("alsa: read: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		h := &inHandler{b: b}
		alsaseq.Walk(buf[:n], h)
		if len(h.msgs) > 0 && b.ToNet != nil {
			b.ToNet(h.msgs)
		}
	}
}

type inHandler struct {
	b    *Bridge
	msgs [][]byte
}

func (h *inHandler) Fixed(evType uint8, src alsaseq.Addr, data []byte) {
	if evType == alsaseq.EvSensing {
		return
	}
	if msg := alsaseq.DecodeMIDI(evType, data); msg != nil {
		h.take(src, msg)
	}
}

func (h *inHandler) VarLen(evType uint8, src alsaseq.Addr, payload []byte) {
	if evType == alsaseq.EvSysEx && len(payload) > 0 {
		h.take(src, append([]byte(nil), payload...))
	}
}

func (h *inHandler) take(src alsaseq.Addr, msg []byte) {
	via := ownPortName
	h.b.mu.Lock()
	if r := h.b.bySrc[src]; r != nil {
		if !r.On {
			h.b.mu.Unlock()
			return
		}
		via = r.Port
		msg = remap(msg, r.Channel)
	}
	h.b.mu.Unlock()
	h.msgs = append(h.msgs, msg)
	if h.b.Monitor != nil {
		h.b.Monitor("out", via, msg)
	}
}

// remap moves a channel message to channel ch (1-16); 0 keeps it.
func remap(msg []byte, ch int) []byte {
	if ch < 1 || ch > 16 || len(msg) == 0 || msg[0] < 0x80 || msg[0] >= 0xF0 {
		return msg
	}
	out := append([]byte(nil), msg...)
	out[0] = msg[0]&0xF0 | byte(ch-1)
	return out
}

// FromNet delivers messages from the network: to whoever listens to the
// hack's port, and to every "in" route's port.
func (b *Bridge) FromNet(msgs [][]byte) {
	b.mu.Lock()
	var ins []*liveRoute
	for _, r := range b.routes {
		if r.Dir == "in" && r.On && r.resolved {
			ins = append(ins, r)
		}
	}
	b.mu.Unlock()
	for _, msg := range msgs {
		if b.Monitor != nil {
			b.Monitor("in", ownPortName, msg)
		}
		b.send(alsaseq.Subscribers, msg)
		for _, r := range ins {
			b.send(r.addr, remap(msg, r.Channel))
		}
	}
}

func (b *Bridge) send(dst alsaseq.Addr, msg []byte) {
	var err error
	if msg[0] == 0xF0 || msg[0] == 0xF7 {
		err = b.c.SendSysEx(dst, msg)
	} else {
		_, err = b.c.SendMIDI(dst, msg)
	}
	if err != nil {
		log.Printf("alsa: send to %d:%d: %v", dst.Client, dst.Port, err)
	}
}

// SetRoutes replaces the route table. Routes that left are unsubscribed;
// the rest keep their state.
func (b *Bridge) SetRoutes(rs []Route) {
	b.mu.Lock()
	old := map[[3]string]*liveRoute{}
	for _, r := range b.routes {
		old[[3]string{r.Client, r.Port, r.Dir}] = r
	}
	var next []*liveRoute
	for _, r := range rs {
		key := [3]string{r.Client, r.Port, r.Dir}
		if lr := old[key]; lr != nil {
			lr.Route = r
			next = append(next, lr)
			delete(old, key)
			continue
		}
		next = append(next, &liveRoute{Route: r})
	}
	for _, lr := range old {
		if lr.Dir == "out" && lr.resolved {
			if err := b.c.Unsubscribe(lr.addr); err != nil {
				log.Printf("alsa: unsubscribe %s: %v", lr.Port, err)
			}
			delete(b.bySrc, lr.addr)
		}
	}
	b.routes = next
	b.mu.Unlock()
	b.resolve()
}

// Routes reports the table with what is resolved.
func (b *Bridge) Routes() []liveRoute {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]liveRoute, len(b.routes))
	for i, r := range b.routes {
		out[i] = *r
	}
	return out
}

func (b *Bridge) resolveLoop() {
	t := time.NewTicker(resolveEach)
	defer t.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-t.C:
			b.mu.Lock()
			need := false
			for _, r := range b.routes {
				if !r.resolved {
					need = true
				}
			}
			b.mu.Unlock()
			if need {
				b.resolve()
			}
		}
	}
}

// resolve finds every route's port and subscribes the "out" ones. A port
// that went away is noticed by a failed subscribe or by its absence from
// the list.
func (b *Bridge) resolve() {
	ports, err := alsaseq.EnumPorts(0)
	if err != nil {
		log.Printf("alsa: list ports: %v", err)
		return
	}
	b.mu.Lock()
	changed := false
	for _, r := range b.routes {
		var found *alsaseq.Port
		for i := range ports {
			if ports[i].ClientName == r.Client && ports[i].PortName == r.Port {
				found = &ports[i]
				break
			}
		}
		if found == nil {
			if r.resolved {
				delete(b.bySrc, r.addr)
				r.resolved = false
				changed = true
				log.Printf("route: %s / %s is gone", r.Client, r.Port)
			}
			continue
		}
		if r.resolved && r.addr == found.Addr {
			continue
		}
		if r.resolved {
			delete(b.bySrc, r.addr)
		}
		r.addr = found.Addr
		if r.Dir == "out" {
			if err := b.c.Subscribe(r.addr); err != nil && !errors.Is(err, syscall.EBUSY) {
				log.Printf("route: subscribe %s / %s: %v", r.Client, r.Port, err)
				continue
			}
			b.bySrc[r.addr] = r
		}
		r.resolved = true
		changed = true
		log.Printf("route: %s / %s is %d:%d (%s)", r.Client, r.Port, r.addr.Client, r.addr.Port, r.Dir)
	}
	routes := make([]liveRoute, len(b.routes))
	for i, r := range b.routes {
		routes[i] = *r
	}
	b.mu.Unlock()
	if changed && b.OnPorts != nil {
		b.OnPorts(routes, true)
	}
}

// Ports lists the ALSA ports a route could use: every port but the hack's
// own and the kernel's.
func (b *Bridge) Ports() []alsaseq.Port {
	ports, err := alsaseq.EnumPorts(0)
	if err != nil {
		return nil
	}
	own := b.c.Addr().Client
	var out []alsaseq.Port
	for _, p := range ports {
		if p.Addr.Client == own || p.Addr.Client == 0 {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Close stops the reader and frees the port.
func (b *Bridge) Close() {
	close(b.done)
	b.c.Close()
}
