package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"rng-chaos/internal/nist"
)

// txStats handles /tx/{id}/stats?mode=nist
func txStats(w http.ResponseWriter, r *http.Request, id string) {
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	q := r.URL.Query()
	mode := q.Get("mode")
	if mode == "" {
		mode = "nist"
	}

	switch mode {
	case "nist":
		// regenerate bits deterministically
		gp := paramsFromTx(tx)
		_, digest := runSimulation(tx.Seed, gp)
		bits := expandBitsFromPathDigest(digest, tx.Count, gp.Whiten)

		// convert bits (0/1) to bytes expected by NIST wrapper
		b := make([]byte, len(bits))
		for i := range bits {
			b[i] = bits[i]
		}

		p, err := nist.RunMonobit(b)
		if err != nil {
			http.Error(w, fmt.Sprintf("nist error: %v", err), http.StatusInternalServerError)
			return
		}
		out := map[string]any{"mode": "nist", "test": "monobit", "p_value": p}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	default:
		http.Error(w, "unsupported stats mode", http.StatusBadRequest)
	}
}

// uploadStatsHandler accepts POSTed stats results (JSON) for ingestion
func uploadStatsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// optional: validate tx id
	if txid, ok := payload["tx_id"].(string); ok && txid != "" {
		// ensure tx exists
		if _, ok := txStore[txid]; !ok {
			http.Error(w, "tx not found", http.StatusNotFound)
			return
		}
	}

	// store uploaded stats as a simple log for now
	log.Printf("uploaded stats: %s", strconv.Quote(string(body)))
	w.WriteHeader(http.StatusNoContent)
}
