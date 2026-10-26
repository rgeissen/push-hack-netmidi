package main

// config.go -- what the user set, kept in <push-hack>/data/netmidi/config.json
// (outside the hack folder, so a reinstall keeps it).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Route bridges one ALSA port to the network ("out") or from it ("in").
// Channel 0 keeps the messages' channels; 1-16 moves them all to that one.
type Route struct {
	Client  string `json:"client"`
	Port    string `json:"port"`
	Dir     string `json:"dir"`
	Channel int    `json:"channel"`
	On      bool   `json:"on"`
}

// Remembered is a peer the hack invites at start and after a drop.
type Remembered struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Config is the whole settings file.
type Config struct {
	Name      string       `json:"name"`       // the Bonjour name
	Port      int          `json:"port"`       // the control port; data is port + 1
	Enabled   bool         `json:"enabled"`    // the session is up and announced
	LatencyMs int          `json:"latency_ms"` // messages from the network wait this long past their time
	ClockIn   bool         `json:"clock_in"`   // clock and transport from the network reach Live (off: Push stays the master)
	Peers     []Remembered `json:"peers"`
	Routes    []Route      `json:"routes"`
}

func defaultConfig() Config {
	return Config{Name: "Push 3", Port: 5004, Enabled: true, LatencyMs: 10}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return defaultConfig(), fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Name == "" {
		cfg.Name = "Push 3"
	}
	if cfg.Port < 1024 || cfg.Port > 65534 || cfg.Port%2 != 0 {
		cfg.Port = 5004
	}
	if cfg.LatencyMs < 0 || cfg.LatencyMs > 500 {
		cfg.LatencyMs = 10
	}
	return cfg, nil
}

// saveConfig writes the file whole, through a rename, so a power cut
// leaves the old one or the new one, never half of either.
func saveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// validRoute says whether a route can be kept.
func validRoute(r Route) error {
	if r.Client == "" || r.Port == "" {
		return errors.New("a route needs a client and a port name")
	}
	if r.Dir != "in" && r.Dir != "out" {
		return errors.New(`a route's dir is "in" or "out"`)
	}
	if r.Channel < 0 || r.Channel > 16 {
		return errors.New("a route's channel is 0 (as is) or 1-16")
	}
	return nil
}
