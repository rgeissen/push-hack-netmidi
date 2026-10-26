// Net MIDI: the Push's MIDI ports on the network as Apple MIDI (RTP-MIDI),
// so an iPad, iPhone or Mac sees the Push as a network MIDI peer.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/federico-pepe/ableton-push-hack/core/alsaseq"
	"github.com/federico-pepe/ableton-push-hack/core/hackcfg"
	"github.com/federico-pepe/ableton-push-hack/core/httpx"
)

const defaultPort = 7713

func main() {
	cfgPath := flag.String("config", "hack.json", "path to hack.json")
	dataDir := flag.String("data", "", "settings folder (default: <push-hack>/data/netmidi, outside the hack folder so a reinstall keeps it)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	cfg, err := hackcfg.Load(*cfgPath, defaultPort)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *showVersion {
		fmt.Println(cfg.Version)
		return
	}
	if *dataDir == "" {
		hackDir, _ := filepath.Abs(filepath.Dir(*cfgPath))
		*dataDir = filepath.Join(filepath.Dir(filepath.Dir(hackDir)), "data", "netmidi")
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	log.Printf("netmidi %s: http :%d, settings in %s", cfg.Version, cfg.Port, *dataDir)

	app := newApp(filepath.Join(*dataDir, "config.json"), cfg.Version)
	app.Start()

	srv := httpx.NewServer(fmt.Sprintf(":%d", cfg.Port), routes(app))
	go func() {
		if err := srv.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
			log.Fatalf("http: %v", err)
		}
	}()

	// The MIDI side waits for the USB-A window after a cold boot; the page
	// and the session are up meanwhile.
	go func() {
		alsaseq.WaitForBootSettle()
		br, err := openBridge()
		if err != nil {
			log.Printf("alsa: %v (no Net MIDI port; the network side still runs)", err)
			return
		}
		log.Printf("alsa: port %q is %d:%d", ownPortName, br.Addr().Client, br.Addr().Port)
		app.BridgeUp(br)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("stopping")
	app.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
