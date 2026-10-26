package main

// mdns.go -- Bonjour through avahi-daemon's D-Bus API. The Push runs avahi
// already (it owns push.local); a second responder next to it could make
// avahi rename the host on a conflict, and /etc/avahi/services is off
// limits. So the hack asks the daemon to announce "_apple-midi._udp" for
// it, and to browse for the same type: that is how the iPad finds the
// Push, and how the Push finds an iPad with its session on.

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	avahiName    = "org.freedesktop.Avahi"
	avahiServer  = "org.freedesktop.Avahi.Server"
	avahiGroup   = "org.freedesktop.Avahi.EntryGroup"
	avahiBrowser = "org.freedesktop.Avahi.ServiceBrowser"
	serviceType  = "_apple-midi._udp"

	ifUnspec     = int32(-1)
	protoInet    = int32(0)
	protoUnspec  = int32(-1)
	lookupOurOwn = uint32(16) // AVAHI_LOOKUP_RESULT_OUR_OWN
	groupEstab   = int32(2)   // AVAHI_ENTRY_GROUP_ESTABLISHED
	groupCollide = int32(3)   // AVAHI_ENTRY_GROUP_COLLISION
	groupFailure = int32(4)   // AVAHI_ENTRY_GROUP_FAILURE
)

// Found is a network MIDI session seen on the network.
type Found struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Addr string `json:"addr"`
	Port int    `json:"port"`
	Own  bool   `json:"own"` // on this very Push (a test instance)
}

// Avahi publishes our service and browses for peers.
type Avahi struct {
	OnFound func(f Found, present bool) // a peer appeared or went away
	OnReset func()                      // avahi restarted: publish again

	conn   *dbus.Conn
	server dbus.BusObject
	sig    chan *dbus.Signal

	mu        sync.Mutex
	group     dbus.ObjectPath
	published string // the name avahi took
	want      string // the name asked for
	port      int
	browser   dbus.ObjectPath
	host      string
}

// NewAvahi connects to the system bus and checks the daemon answers.
func NewAvahi() (*Avahi, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, fmt.Errorf("system bus: %w", err)
	}
	a := &Avahi{conn: conn, server: conn.Object(avahiName, "/"), sig: make(chan *dbus.Signal, 64)}
	var version string
	if err := a.server.Call(avahiServer+".GetVersionString", 0).Store(&version); err != nil {
		conn.Close()
		return nil, fmt.Errorf("avahi: %w", err)
	}
	a.server.Call(avahiServer+".GetHostNameFqdn", 0).Store(&a.host)
	for _, o := range []dbus.MatchOption{
		dbus.WithMatchInterface(avahiBrowser),
		dbus.WithMatchInterface(avahiGroup),
	} {
		if err := conn.AddMatchSignal(o); err != nil {
			conn.Close()
			return nil, fmt.Errorf("avahi signals: %w", err)
		}
	}
	conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, avahiName))
	conn.Signal(a.sig)
	go a.signals()
	log.Printf("mdns: %s on %s", version, a.host)
	return a, nil
}

// Host is the daemon's host name, push.local.
func (a *Avahi) Host() string { return a.host }

// Published is the name the service is announced under, "" when it is not.
func (a *Avahi) Published() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.published
}

// Publish announces the service. A name another host took gets " (2)"
// and so on, as Apple's hosts do.
func (a *Avahi) Publish(name string, port int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.want, a.port = name, port
	return a.publish(name, port, 1)
}

func (a *Avahi) publish(name string, port, try int) error {
	a.unpublish()
	var path dbus.ObjectPath
	if err := a.server.Call(avahiServer+".EntryGroupNew", 0).Store(&path); err != nil {
		return fmt.Errorf("avahi: EntryGroupNew: %w", err)
	}
	a.group = path
	full := name
	if try > 1 {
		full = fmt.Sprintf("%s (%d)", name, try)
	}
	g := a.conn.Object(avahiName, path)
	err := g.Call(avahiGroup+".AddService", 0, ifUnspec, protoUnspec, uint32(0),
		full, serviceType, "", "", uint16(port), [][]byte{}).Err
	if err == nil {
		err = g.Call(avahiGroup+".Commit", 0).Err
	}
	if err != nil {
		var de dbus.Error
		if errors.As(err, &de) && strings.HasSuffix(de.Name, ".CollisionError") && try < 10 {
			return a.publish(name, port, try+1)
		}
		a.unpublish()
		return fmt.Errorf("avahi: AddService %q: %w", full, err)
	}
	a.published = full
	return nil
}

// Unpublish withdraws the service.
func (a *Avahi) Unpublish() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.want = ""
	a.unpublish()
}

func (a *Avahi) unpublish() {
	if a.group != "" {
		a.conn.Object(avahiName, a.group).Call(avahiGroup+".Free", 0)
		a.group = ""
	}
	a.published = ""
}

// Browse starts looking for network MIDI sessions, IPv4 only.
func (a *Avahi) Browse() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.browse()
}

func (a *Avahi) browse() error {
	var path dbus.ObjectPath
	if err := a.server.Call(avahiServer+".ServiceBrowserNew", 0,
		ifUnspec, protoInet, serviceType, "", uint32(0)).Store(&path); err != nil {
		return fmt.Errorf("avahi: ServiceBrowserNew: %w", err)
	}
	a.browser = path
	return nil
}

func (a *Avahi) signals() {
	for s := range a.sig {
		switch s.Name {
		case avahiBrowser + ".ItemNew", avahiBrowser + ".ItemRemove":
			if len(s.Body) < 6 {
				continue
			}
			iface, _ := s.Body[0].(int32)
			proto, _ := s.Body[1].(int32)
			name, _ := s.Body[2].(string)
			flags, _ := s.Body[5].(uint32)
			if flags&lookupOurOwn != 0 {
				continue
			}
			if strings.HasSuffix(s.Name, ".ItemRemove") {
				if a.OnFound != nil {
					a.OnFound(Found{Name: name}, false)
				}
				continue
			}
			go a.resolve(iface, proto, name)
		case avahiGroup + ".StateChanged":
			if len(s.Body) < 1 {
				continue
			}
			state, _ := s.Body[0].(int32)
			a.mu.Lock()
			mine := s.Path == a.group
			want, port := a.want, a.port
			a.mu.Unlock()
			if !mine {
				continue
			}
			switch state {
			case groupCollide:
				log.Printf("mdns: name %q taken, trying another", want)
				a.mu.Lock()
				if err := a.publish(want, port, 2); err != nil {
					log.Printf("mdns: %v", err)
				}
				a.mu.Unlock()
			case groupFailure:
				msg, _ := s.Body[1].(string)
				log.Printf("mdns: announce failed: %s", msg)
			}
		case "org.freedesktop.DBus.NameOwnerChanged":
			if len(s.Body) < 3 {
				continue
			}
			if newOwner, _ := s.Body[2].(string); newOwner != "" {
				log.Printf("mdns: avahi restarted, announcing again")
				a.mu.Lock()
				a.group, a.published, a.browser = "", "", ""
				want, port := a.want, a.port
				a.mu.Unlock()
				if want != "" {
					if err := a.Publish(want, port); err != nil {
						log.Printf("mdns: %v", err)
					}
				}
				if err := a.Browse(); err != nil {
					log.Printf("mdns: %v", err)
				}
				if a.OnReset != nil {
					a.OnReset()
				}
			}
		}
	}
}

func (a *Avahi) resolve(iface, proto int32, name string) {
	var (
		rIface, rProto, aProto int32
		rName, rType, domain   string
		host, addr             string
		port                   uint16
		txt                    [][]byte
		flags                  uint32
	)
	err := a.server.Call(avahiServer+".ResolveService", 0,
		iface, proto, name, serviceType, "", protoInet, uint32(0)).Store(
		&rIface, &rProto, &rName, &rType, &domain, &host, &aProto, &addr, &port, &txt, &flags)
	if err != nil {
		log.Printf("mdns: resolve %q: %v", name, err)
		return
	}
	if a.OnFound != nil {
		a.OnFound(Found{Name: rName, Host: host, Addr: addr, Port: int(port),
			Own: strings.EqualFold(host, a.host)}, true)
	}
}

// Close withdraws the service and leaves the bus.
func (a *Avahi) Close() {
	a.Unpublish()
	a.mu.Lock()
	if a.browser != "" {
		a.conn.Object(avahiName, a.browser).Call(avahiBrowser+".Free", 0)
		a.browser = ""
	}
	a.mu.Unlock()
	a.conn.RemoveSignal(a.sig)
	a.conn.Close()
}
