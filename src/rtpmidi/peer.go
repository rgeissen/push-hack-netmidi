package rtpmidi

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Transport is an Endpoint on two UDP sockets. Events reach OnEvent on
// the transport's goroutines, with no lock held; keep it quick.
type Transport struct {
	OnEvent func(Event)

	mu   sync.Mutex
	ep   *Endpoint
	ctrl *net.UDPConn
	data *net.UDPConn
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// Listen binds the control port and the data port (port + 1), IPv4 only,
// and starts the session.
func Listen(port int, name string) (*Transport, error) {
	ctrl, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if err != nil {
		return nil, fmt.Errorf("control port %d: %w", port, err)
	}
	data, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port + 1})
	if err != nil {
		ctrl.Close()
		return nil, fmt.Errorf("data port %d: %w", port+1, err)
	}
	t := &Transport{ep: NewEndpoint(name, time.Now()), ctrl: ctrl, data: data, done: make(chan struct{})}
	t.wg.Add(3)
	go t.read(ctrl, PortControl)
	go t.read(data, PortData)
	go t.tick()
	return t, nil
}

// SSRC is the session's.
func (t *Transport) SSRC() uint32 { return t.ep.SSRC }

// Port is the control port.
func (t *Transport) Port() int { return t.ctrl.LocalAddr().(*net.UDPAddr).Port }

func (t *Transport) read(c *net.UDPConn, port Port) {
	defer t.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, from, err := c.ReadFromUDPAddrPort(buf)
		if err != nil {
			select {
			case <-t.done:
				return
			default:
			}
			log.Printf("rtpmidi: read %v: %v", port, err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		t.mu.Lock()
		outs, evs := t.ep.Receive(port, from, buf[:n], time.Now())
		t.mu.Unlock()
		t.deliver(outs, evs)
	}
}

func (t *Transport) tick() {
	defer t.wg.Done()
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-t.done:
			return
		case now := <-tk.C:
			t.mu.Lock()
			outs, evs := t.ep.Tick(now)
			t.mu.Unlock()
			t.deliver(outs, evs)
		}
	}
}

func (t *Transport) deliver(outs []Out, evs []Event) {
	for _, o := range outs {
		c := t.ctrl
		if o.Port == PortData {
			c = t.data
		}
		if _, err := c.WriteToUDPAddrPort(o.Data, o.To); err != nil {
			log.Printf("rtpmidi: send to %v: %v", o.To, err)
		}
	}
	if t.OnEvent != nil {
		for _, ev := range evs {
			t.OnEvent(ev)
		}
	}
}

// Invite starts a session with the peer at a control address.
func (t *Transport) Invite(ctrl netip.AddrPort) {
	t.mu.Lock()
	outs := t.ep.Invite(ctrl, time.Now())
	t.mu.Unlock()
	t.deliver(outs, nil)
}

// Bye ends the session with a peer.
func (t *Transport) Bye(ssrc uint32) {
	t.mu.Lock()
	outs, evs := t.ep.Bye(ssrc, time.Now())
	t.mu.Unlock()
	t.deliver(outs, evs)
}

// Send sends messages to every connected peer.
func (t *Transport) Send(msgs [][]byte) {
	t.mu.Lock()
	outs := t.ep.Send(msgs, time.Now())
	t.mu.Unlock()
	t.deliver(outs, nil)
}

// Peers lists the peers.
func (t *Transport) Peers() []Peer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ep.Peers()
}

// Connected counts the connected peers.
func (t *Transport) Connected() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ep.Connected()
}

// Close says bye to every peer and frees the ports.
func (t *Transport) Close() {
	t.once.Do(func() {
		t.mu.Lock()
		outs, evs := t.ep.ByeAll(time.Now())
		t.mu.Unlock()
		t.deliver(outs, evs)
		close(t.done)
		t.ctrl.Close()
		t.data.Close()
		t.wg.Wait()
	})
}
