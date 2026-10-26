package main

// app.go -- the hack's state: the session (up or down), its peers, what
// Bonjour found, the routes, and the monitor. The web page and the Shadow
// tab both read and change it here; the bridge and the transport report
// into it.

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/sse"
	"github.com/rgeissen/push-hack-netmidi/rtpmidi"
)

const (
	monitorKeep   = 60
	broadcastEach = 250 * time.Millisecond
	rememberEach  = 15 * time.Second
)

// PeerView is a peer as the page and the tab show it.
type PeerView struct {
	SSRC      uint32    `json:"ssrc"`
	Name      string    `json:"name"`
	Addr      string    `json:"addr"`
	State     string    `json:"state"`
	Initiator bool      `json:"initiator"`
	Since     time.Time `json:"since"`
	LatencyMs float64   `json:"latency_ms"`
	In        uint64    `json:"in"`
	Out       uint64    `json:"out"`
}

// RouteView is a route with whether its port is there.
type RouteView struct {
	Route
	Resolved bool `json:"resolved"`
}

// MonLine is one message in the monitor.
type MonLine struct {
	T   string `json:"t"`
	Dir string `json:"dir"` // "in" from the network, "out" to it
	Via string `json:"via"` // the ALSA port
	Hex string `json:"hex"`
}

// State is everything the page shows.
type State struct {
	Version    string       `json:"version"`
	Name       string       `json:"name"`
	Port       int          `json:"port"`
	Enabled    bool         `json:"enabled"`
	Announced  string       `json:"announced"` // the Bonjour name in use, "" when not announced
	Host       string       `json:"host"`
	Error      string       `json:"error"`
	Alsa       bool         `json:"alsa"` // the Net MIDI port is open
	OwnPort    string       `json:"own_port"`
	Peers      []PeerView   `json:"peers"`
	Found      []Found      `json:"found"`
	Remembered []Remembered `json:"remembered"`
	Routes     []RouteView  `json:"routes"`
	Monitor    []MonLine    `json:"monitor"`
}

// App holds it all.
type App struct {
	version string
	cfgPath string
	broker  *sse.Broker[State]

	mu       sync.Mutex
	cfg      Config
	tr       *rtpmidi.Transport
	av       *Avahi
	br       *Bridge
	found    map[string]Found
	monitor  []MonLine
	errText  string
	dirty    atomic.Bool
	cursor   int       // the Shadow tab's
	offArmed time.Time // when OFF was pressed once on the tab
	done     chan struct{}
}

func newApp(cfgPath, version string) *App {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Printf("config: %v (defaults)", err)
	}
	return &App{version: version, cfgPath: cfgPath, cfg: cfg,
		broker: sse.NewBroker[State](8, true), found: map[string]Found{}, done: make(chan struct{})}
}

// Start brings up Bonjour and, if enabled, the session.
func (a *App) Start() {
	av, err := NewAvahi()
	if err != nil {
		log.Printf("mdns: %v (no Bonjour: the iPad has to be given this Push's address)", err)
	} else {
		av.OnFound = a.onFound
		av.OnReset = func() { a.touch() }
		if err := av.Browse(); err != nil {
			log.Printf("mdns: %v", err)
		}
	}
	a.mu.Lock()
	a.av = av
	if a.cfg.Enabled {
		a.up()
	}
	a.mu.Unlock()
	go a.loop()
}

// Stop says bye to the peers and frees everything.
func (a *App) Stop() {
	close(a.done)
	a.mu.Lock()
	old := a.down()
	a.mu.Unlock()
	closeSession(old)
	a.mu.Lock()
	if a.av != nil {
		a.av.Close()
	}
	if a.br != nil {
		a.br.Close()
	}
	a.mu.Unlock()
}

// up starts the session. Caller holds a.mu.
func (a *App) up() {
	if a.tr != nil {
		return
	}
	tr, err := rtpmidi.Listen(a.cfg.Port, a.cfg.Name)
	if err != nil {
		a.errText = err.Error()
		log.Printf("session: %v", err)
		return
	}
	tr.OnEvent = a.onEvent
	a.tr = tr
	a.errText = ""
	log.Printf("session: %q on UDP %d/%d, SSRC %08x", a.cfg.Name, a.cfg.Port, a.cfg.Port+1, tr.SSRC())
	if a.av != nil {
		if err := a.av.Publish(a.cfg.Name, a.cfg.Port); err != nil {
			a.errText = err.Error()
			log.Printf("mdns: %v", err)
		} else {
			log.Printf("mdns: announced as %q", a.av.Published())
		}
	}
	for _, r := range a.cfg.Peers {
		go a.invite(r.Host, r.Port)
	}
}

// down takes the session out of the app and withdraws its announcement.
// Caller holds a.mu, and closes what comes back after letting it go:
// Close says bye to every peer, and their disconnect events come straight
// back into the app.
func (a *App) down() *rtpmidi.Transport {
	if a.av != nil {
		a.av.Unpublish()
	}
	tr := a.tr
	a.tr = nil
	return tr
}

func closeSession(tr *rtpmidi.Transport) {
	if tr != nil {
		tr.Close()
		log.Printf("session: closed")
	}
}

// BridgeUp takes the ALSA side once it is open (after boot settle).
func (a *App) BridgeUp(br *Bridge) {
	br.ToNet = a.toNet
	br.Monitor = a.monitorLine
	br.OnPorts = func([]liveRoute, bool) { a.touch() }
	a.mu.Lock()
	a.br = br
	routes := a.cfg.Routes
	a.mu.Unlock()
	br.SetRoutes(routes)
	go br.Run()
	a.touch()
}

func (a *App) toNet(msgs [][]byte) {
	a.mu.Lock()
	tr := a.tr
	a.mu.Unlock()
	if tr != nil {
		tr.Send(msgs)
	}
}

func (a *App) onEvent(ev rtpmidi.Event) {
	if ev.Kind == rtpmidi.EvMIDI {
		a.mu.Lock()
		br := a.br
		a.mu.Unlock()
		if br != nil {
			br.FromNet(ev.Messages)
		}
		return
	}
	log.Printf("peer %q at %s: %s", ev.Peer.Name, ev.Peer.Ctrl, ev.Kind)
	a.touch()
}

// onFound keeps the peers Bonjour sees. A peer on two interfaces comes
// twice; the second one (its address may differ) replaces the first, and
// only a change is logged.
func (a *App) onFound(f Found, present bool) {
	a.mu.Lock()
	old, had := a.found[f.Name]
	if present {
		a.found[f.Name] = f
	} else {
		delete(a.found, f.Name)
	}
	a.mu.Unlock()
	switch {
	case present && (!had || old.Addr != f.Addr || old.Port != f.Port):
		log.Printf("mdns: found %q at %s:%d (%s)", f.Name, f.Addr, f.Port, f.Host)
	case !present && had:
		log.Printf("mdns: %q went away", f.Name)
	}
	a.touch()
}

func (a *App) monitorLine(dir, via string, msg []byte) {
	if len(msg) == 1 && (msg[0] == 0xF8 || msg[0] == 0xFE) {
		return // clock and sensing would drown the rest
	}
	line := MonLine{T: time.Now().Format("15:04:05.000"), Dir: dir, Via: via, Hex: hex.EncodeToString(msg)}
	a.mu.Lock()
	a.monitor = append(a.monitor, line)
	if len(a.monitor) > monitorKeep {
		a.monitor = a.monitor[len(a.monitor)-monitorKeep:]
	}
	a.mu.Unlock()
	a.touch()
}

// touch marks the state changed; loop sends it to the page soon. It takes
// no lock: the transport reports events from inside calls made under a.mu.
func (a *App) touch() { a.dirty.Store(true) }

// loop sends changed state to the page, and invites remembered peers that
// are not connected.
func (a *App) loop() {
	send := time.NewTicker(broadcastEach)
	remember := time.NewTicker(rememberEach)
	defer send.Stop()
	defer remember.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-send.C:
			if a.dirty.Swap(false) {
				a.broker.Broadcast(a.State())
			}
		case <-remember.C:
			a.mu.Lock()
			tr := a.tr
			var again []Remembered
			if tr != nil {
				have := map[string]bool{}
				for _, p := range tr.Peers() {
					have[p.Ctrl.String()] = true
				}
				for _, r := range a.cfg.Peers {
					if ap, ok := resolve(r.Host, r.Port); !ok || !have[ap.String()] {
						again = append(again, r)
					}
				}
			}
			a.mu.Unlock()
			for _, r := range again {
				go a.invite(r.Host, r.Port)
			}
		}
	}
}

func resolve(host string, port int) (netip.AddrPort, bool) {
	if ap, err := netip.ParseAddrPort(fmt.Sprintf("%s:%d", host, port)); err == nil {
		return ap, true
	}
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return netip.AddrPort{}, false
	}
	return addr.AddrPort(), true
}

func (a *App) invite(host string, port int) {
	ap, ok := resolve(host, port)
	if !ok {
		log.Printf("connect: cannot resolve %s", host)
		return
	}
	a.mu.Lock()
	tr := a.tr
	a.mu.Unlock()
	if tr != nil {
		tr.Invite(ap)
	}
}

// SetSession changes name, port or on/off; nil leaves a field as it is.
func (a *App) SetSession(name *string, port *int, enabled *bool) error {
	a.mu.Lock()
	var old *rtpmidi.Transport
	defer func() { a.mu.Unlock(); closeSession(old) }()
	restart := false
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || len(n) > 63 {
			return fmt.Errorf("the name is 1 to 63 characters")
		}
		restart = restart || n != a.cfg.Name
		a.cfg.Name = n
	}
	if port != nil {
		if *port < 1024 || *port > 65534 || *port%2 != 0 {
			return fmt.Errorf("the port is an even number from 1024 to 65534")
		}
		restart = restart || *port != a.cfg.Port
		a.cfg.Port = *port
	}
	if enabled != nil {
		a.cfg.Enabled = *enabled
	}
	if restart || !a.cfg.Enabled {
		old = a.down()
	}
	if a.cfg.Enabled && a.tr == nil {
		// A port change needs the old sockets gone first: close now,
		// before the lock goes, since nobody else can see it any more.
		closeSession(old)
		old = nil
		a.up()
	}
	a.touch()
	return a.save()
}

// Connect invites a peer; remember keeps it for the next start.
func (a *App) Connect(host string, port int, remember bool, name string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("a host is needed")
	}
	if port <= 0 {
		port = 5004
	}
	a.mu.Lock()
	if a.tr == nil {
		a.mu.Unlock()
		return fmt.Errorf("the session is off")
	}
	if remember {
		known := false
		for _, r := range a.cfg.Peers {
			if r.Host == host && r.Port == port {
				known = true
			}
		}
		if !known {
			a.cfg.Peers = append(a.cfg.Peers, Remembered{Name: name, Host: host, Port: port})
			a.save()
		}
	}
	a.mu.Unlock()
	go a.invite(host, port)
	return nil
}

// Disconnect says bye to a peer.
func (a *App) Disconnect(ssrc uint32) {
	a.mu.Lock()
	tr := a.tr
	a.mu.Unlock()
	if tr != nil {
		tr.Bye(ssrc)
	}
}

// Forget drops a remembered peer.
func (a *App) Forget(host string, port int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var keep []Remembered
	for _, r := range a.cfg.Peers {
		if r.Host != host || r.Port != port {
			keep = append(keep, r)
		}
	}
	a.cfg.Peers = keep
	a.touch()
	return a.save()
}

// SetRoutes replaces the route table.
func (a *App) SetRoutes(rs []Route) error {
	for _, r := range rs {
		if err := validRoute(r); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.cfg.Routes = rs
	br := a.br
	err := a.save()
	a.touch()
	a.mu.Unlock()
	if br != nil {
		br.SetRoutes(rs)
	}
	return err
}

// save writes the config. Caller holds a.mu.
func (a *App) save() error {
	if err := saveConfig(a.cfgPath, a.cfg); err != nil {
		log.Printf("config: %v", err)
		return err
	}
	return nil
}

// Ports lists the ALSA ports a route could use.
func (a *App) Ports() []portView {
	a.mu.Lock()
	br := a.br
	a.mu.Unlock()
	if br == nil {
		return nil
	}
	var out []portView
	for _, p := range br.Ports() {
		out = append(out, portView{Client: p.ClientName, Port: p.PortName,
			Addr: fmt.Sprintf("%d:%d", p.Addr.Client, p.Addr.Port), Caps: capsOf(p.Caps)})
	}
	return out
}

type portView struct {
	Client string `json:"client"`
	Port   string `json:"port"`
	Addr   string `json:"addr"`
	Caps   string `json:"caps"` // "R" sends (an "out" route reads it), "W" receives
}

func capsOf(c uint32) string {
	s := ""
	if c&0x01 != 0 { // alsaseq.CapRead
		s += "R"
	}
	if c&0x02 != 0 { // alsaseq.CapWrite
		s += "W"
	}
	return s
}

// State is a snapshot for the page and the tab.
func (a *App) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := State{Version: a.version, Name: a.cfg.Name, Port: a.cfg.Port, Enabled: a.cfg.Enabled,
		Error: a.errText, Alsa: a.br != nil, OwnPort: ownPortName,
		Remembered: append([]Remembered{}, a.cfg.Peers...), Monitor: append([]MonLine{}, a.monitor...)}
	if a.av != nil {
		s.Announced = a.av.Published()
		s.Host = a.av.Host()
	}
	if a.tr != nil {
		for _, p := range a.tr.Peers() {
			s.Peers = append(s.Peers, PeerView{SSRC: p.SSRC, Name: p.Name, Addr: p.Ctrl.String(),
				State: p.State.String(), Initiator: p.Initiator, Since: p.Since,
				LatencyMs: float64(p.Latency.Microseconds()) / 1000, In: p.In, Out: p.Out})
		}
		sort.Slice(s.Peers, func(i, j int) bool { return s.Peers[i].Addr < s.Peers[j].Addr })
	}
	for _, f := range a.found {
		s.Found = append(s.Found, f)
	}
	sort.Slice(s.Found, func(i, j int) bool { return s.Found[i].Name < s.Found[j].Name })
	if a.br != nil {
		for _, r := range a.br.Routes() {
			s.Routes = append(s.Routes, RouteView{Route: r.Route, Resolved: r.resolved})
		}
	} else {
		for _, r := range a.cfg.Routes {
			s.Routes = append(s.Routes, RouteView{Route: r})
		}
	}
	return s
}
