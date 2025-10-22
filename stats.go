package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

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

		// pack bits MSB-first into bytes (same format as /tx/{id}/bin)
		var cur byte
		used := 0
		out := make([]byte, 0, len(bits)/8+1)
		for _, b := range bits {
			cur = (cur << 1) | (b & 1)
			used++
			if used == 8 {
				out = append(out, cur)
				cur, used = 0, 0
			}
		}
		if used > 0 {
			cur <<= uint(8 - used)
			out = append(out, cur)
		}

		// write to temp file
		tmpDir := os.TempDir()
		tmpFile := filepath.Join(tmpDir, fmt.Sprintf("rngchaos_tx_%s.bin", tx.TxID))
		if err := os.WriteFile(tmpFile, out, 0o600); err != nil {
			http.Error(w, fmt.Sprintf("failed to write temp file: %v", err), http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmpFile)

		// find external command: prefer env var NIST_STS_CMD, else try common names/locations
		cmdPath := os.Getenv("NIST_STS_CMD")
		candidates := []string{}
		if cmdPath == "" {
			candidates = append(candidates,
				"sts",
				"assess",
				filepath.Join("third_party", "NIST-Statistical-Test-Suite", "assess"),
				filepath.Join("third_party", "NIST-Statistical-Test-Suite", "sts"),
			)
		} else {
			candidates = append(candidates, cmdPath)
		}

		var found string
		for _, c := range candidates {
			if p, err := exec.LookPath(c); err == nil {
				found = p
				break
			}
		}

		if found == "" {
			// fallback to CGO wrapper when external binary not configured/found
			// convert bits to byte slice of 0/1 for internal nist wrapper
			bflat := make([]byte, len(bits))
			for i := range bits {
				bflat[i] = bits[i]
			}
			pval, err := nist.RunMonobit(bflat)
			if err != nil {
				http.Error(w, fmt.Sprintf("nist not available (no external cmd and cgo test failed): %v", err), http.StatusInternalServerError)
				return
			}
			jout := map[string]any{"mode": "nist", "test": "monobit", "p_value": pval, "source": "cgo"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jout)
			return
		}

		// run external command with a timeout
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, found, tmpFile)
		// ensure working directory is project root so relative configs resolve
		cmd.Dir = "."
		outBuf, err := cmd.CombinedOutput()
		resp := map[string]any{"mode": "nist", "cmd": found, "file": tmpFile, "output": string(outBuf)}
		if err != nil {
			resp["error"] = err.Error()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
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
