package main

// web.go -- the HTTP API and the page. The page is one embedded file; the
// Shadow tab is in shadow.go.

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/federico-pepe/ableton-push-hack/core/httpx"
	"github.com/federico-pepe/ableton-push-hack/core/sse"
)

//go:embed ui/index.html
var pageHTML []byte

func routes(a *App) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(pageHTML)
	})

	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, a.State())
	})

	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		ch := a.broker.Register()
		defer a.broker.Unregister(ch)
		ch <- a.State() // the full picture first, then changes
		sse.Serve(w, r, ch, func(s State) ([]byte, error) { return json.Marshal(s) })
	})

	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name    *string `json:"name"`
			Port    *int    `json:"port"`
			Enabled *bool   `json:"enabled"`
		}
		if !decode(w, r, &body) {
			return
		}
		if err := a.SetSession(body.Name, body.Port, body.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		httpx.JSON(w, a.State())
	})

	mux.HandleFunc("POST /api/connect", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Name     string `json:"name"`
			Remember bool   `json:"remember"`
		}
		if !decode(w, r, &body) {
			return
		}
		if err := a.Connect(body.Host, body.Port, body.Remember, body.Name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/disconnect", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SSRC uint32 `json:"ssrc"`
		}
		if !decode(w, r, &body) {
			return
		}
		a.Disconnect(body.SSRC)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/forget", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		if !decode(w, r, &body) {
			return
		}
		if err := a.Forget(body.Host, body.Port); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/ports", func(w http.ResponseWriter, r *http.Request) {
		ports := a.Ports()
		if ports == nil {
			ports = []portView{}
		}
		httpx.JSON(w, ports)
	})

	mux.HandleFunc("POST /api/routes", func(w http.ResponseWriter, r *http.Request) {
		var rs []Route
		if !decode(w, r, &rs) {
			return
		}
		if err := a.SetRoutes(rs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		httpx.JSON(w, a.State())
	})

	mux.HandleFunc("GET /api/shadow", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, a.shadowView())
	})
	mux.HandleFunc("POST /api/shadow", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CC    int `json:"cc"`
			Value int `json:"value"`
		}
		if !decode(w, r, &body) {
			return
		}
		a.shadowPress(body.CC, body.Value)
		w.WriteHeader(http.StatusNoContent)
	})

	return httpx.WithCORS("GET, POST", mux)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}
