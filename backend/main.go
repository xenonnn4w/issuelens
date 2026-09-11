package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if os.Getenv("DEPLOYED") == "1" && os.Getenv("GATEWAY_TOKEN") == "" {
		log.Fatal("Deployed mode requires GATEWAY_TOKEN and a trusted reverse proxy")
	}
	dbPath := env("ISSUE_LENS_DB", "data/investigations.sqlite3")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		log.Fatal(err)
	}
	store, err := openStore(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	server := newServer(store, env("WEB_ROOT", "../web/out"))
	addr := env("LISTEN_ADDR", "127.0.0.1:8765")
	httpServer := &http.Server{Addr: addr, Handler: server.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("Issue Lens listening on %s", addr)
	log.Fatal(httpServer.ListenAndServe())
}
