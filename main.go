package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/rs/cors"
)

func main() {
	// try load persisted store
	if err := loadStore(); err != nil {
		log.Printf("no persisted store loaded: %v", err)
		// attempt to create an empty store.json so future saves have a valid target
		p := persistedStore{TxStore: map[string]*Transaction{}, Chain: []Block{}}
		if data, err := json.MarshalIndent(p, "", "  "); err == nil {
			if err := os.WriteFile(storePath(), data, 0o644); err == nil {
				log.Printf("created new empty store.json at %s", storePath())
			} else {
				log.Printf("failed to write empty store.json: %v", err)
			}
		} else {
			log.Printf("failed to marshal empty store: %v", err)
		}
	} else {
		log.Printf("loaded persisted store from %s", storePath())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/generate", generateHandler)
	mux.HandleFunc("/generate-tier", generateTierHandler)
	mux.HandleFunc("/tx/", txRouter)
	mux.HandleFunc("/txs", txsHandler)
	mux.HandleFunc("/chain", chainHandler)
	mux.HandleFunc("/stats/upload", uploadStatsHandler)
	c := cors.New(cors.Options{
		AllowOriginFunc:  nil,
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: false,
	})
	handler := c.Handler(mux)

	srv := &http.Server{
		Addr:              ":4040",
		Handler:           handler,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      600 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("rng-chaos server on %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
