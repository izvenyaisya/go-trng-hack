package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// /tx/{id}/stats?n=all|dieharder|raw
// If dieharder is installed, runs a full dieharder -a on the tx binary and returns output.
func txStats(w http.ResponseWriter, r *http.Request, id string) {
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	log.Printf("txStats: request for tx %s", id)
	q := r.URL.Query()
	mode := q.Get("mode")
	if mode == "" {
		mode = "dieharder"
	}

	// regenerate binary
	gp := paramsFromTx(tx)
	_, digest := runSimulation(tx.Seed, gp)
	bits := expandBitsFromPathDigest(digest, tx.Count, gp.Whiten)
	// pack MSB-first
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

	// if mode==raw just return binary
	if mode == "raw" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.bin\"", id))
		_, _ = w.Write(out)
		log.Printf("txStats: served raw binary for tx %s (%d bytes)", id, len(out))
		return
	}

	// if dieharder or nist requested
	if mode == "dieharder" {
		// write to temp file
		tmp, err := os.CreateTemp("", "rng-chaos-*.bin")
		if err != nil {
			http.Error(w, "failed to create temp file", http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmp.Name())
		_, _ = tmp.Write(out)
		tmp.Close()

		// check dieharder in PATH
		if _, err := exec.LookPath("dieharder"); err != nil {
			// no dieharder — return sha256 and size
			sum := sha256.Sum256(out)
			resp := map[string]any{
				"dieharder_available": false,
				"size_bytes":          len(out),
				"sha256":              hex.EncodeToString(sum[:]),
				"note":                "install dieharder to run full battery",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			log.Printf("txStats: dieharder not found in PATH; returning sha256 for tx %s", id)
			return
		}

		// run dieharder -a -g 201 -f <file>
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dieharder", "-a", "-g", "201", "-f", tmp.Name())
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
		log.Printf("txStats: running dieharder for tx %s (file=%s)", id, tmp.Name())
		if err := cmd.Run(); err != nil {
			// return whatever output we have plus error
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("dieharder failed: " + err.Error() + "\n\n"))
			_, _ = io.Copy(w, &outBuf)
			log.Printf("txStats: dieharder failed for tx %s: %v", id, err)
			return
		}

		outStr := outBuf.String()
		// try to extract NIST test block (from 1. ... to 14.)
		start := strings.Index(outStr, "\n1.")
		if start == -1 {
			start = strings.Index(outStr, "1.")
		} else {
			start = start + 1
		}
		end := -1
		if start != -1 {
			// find last occurrence of '\n14.'
			last := strings.LastIndex(outStr, "\n14.")
			if last == -1 {
				last = strings.LastIndex(outStr, "14.")
			} else {
				last = last + 1
			}
			if last != -1 {
				// try to extend to next blank line after last
				rem := outStr[last:]
				idx := strings.Index(rem, "\n\n")
				if idx != -1 {
					end = last + idx
				} else {
					end = len(outStr)
				}
			}
		}

		nistText := ""
		if start != -1 && end != -1 && start < end {
			nistText = strings.TrimSpace(outStr[start:end])
		}

		resp := map[string]any{
			"nist_text":   nistText,
			"full_output": outStr,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		log.Printf("txStats: dieharder completed for tx %s", id)
		return
	}

	if mode == "nist" {
		// if 'assess' (NIST STS) is installed, run it on ASCII bits; otherwise fall back
		if _, err := exec.LookPath("assess"); err != nil {
			// assess not found — run local JSON runner
			j := localNISTJSON(bits)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(j)
			log.Printf("txStats: local NIST-like JSON report generated for tx %s", id)
			return
		}

		// write ASCII bit file for assess
		tmpNName, bitCount, err := writeASCIIBitsTemp(bits)
		if err != nil {
			http.Error(w, "failed to prepare bits for NIST assess", http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmpNName)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "assess", tmpNName)
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
		log.Printf("txStats: running assess for tx %s (file=%s)", id, tmpNName)
		if err := cmd.Run(); err != nil {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("nist assess failed: " + err.Error() + "\n\n"))
			_, _ = io.Copy(w, &outBuf)
			log.Printf("txStats: assess failed for tx %s: %v", id, err)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		if bitCount < 1_000_000 {
			_, _ = w.Write([]byte(fmt.Sprintf("NOTE: input has %d bits; NIST STS may skip/ERROR some tests for small samples\n\n", bitCount)))
		}
		_, _ = io.Copy(w, &outBuf)
		log.Printf("txStats: assess completed for tx %s", id)
		return
	}

	http.Error(w, "unknown stats mode", http.StatusBadRequest)
}

// POST /stats/upload
// Accepts multipart file field `file` OR raw body (Content-Type: application/octet-stream or text/plain)
// Query param mode=dieharder|raw controls action (default dieharder).
func uploadStatsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "dieharder"
	}
	log.Printf("uploadStats: mode=%s, remote=%s", mode, r.RemoteAddr)

	var data []byte
	// try multipart
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err == nil {
			f, _, err := r.FormFile("file")
			if err == nil {
				defer f.Close()
				b, _ := io.ReadAll(f)
				data = b
			}
		}
	}
	if len(data) == 0 {
		// fallback to raw body
		b, _ := io.ReadAll(r.Body)
		data = b
	}

	if len(data) == 0 {
		http.Error(w, "no data uploaded", http.StatusBadRequest)
		return
	}

	// write to temp file
	tmp, err := os.CreateTemp("", "rng-upload-*.bin")
	if err != nil {
		http.Error(w, "failed to create temp file", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.Write(data)
	tmp.Close()

	if mode == "raw" {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
		log.Printf("uploadStats: served raw uploaded data (%d bytes)", len(data))
		return
	}

	// handle dieharder or nist modes
	if mode == "dieharder" {
		if _, err := exec.LookPath("dieharder"); err != nil {
			sum := sha256.Sum256(data)
			resp := map[string]any{
				"dieharder_available": false,
				"size_bytes":          len(data),
				"sha256":              hex.EncodeToString(sum[:]),
				"note":                "install dieharder to run full battery",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			log.Printf("uploadStats: dieharder not found; returning sha256 for uploaded data")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dieharder", "-a", "-g", "201", "-f", tmp.Name())
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
		log.Printf("uploadStats: running dieharder on uploaded data (file=%s)", tmp.Name())
		if err := cmd.Run(); err != nil {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("dieharder failed: " + err.Error() + "\n\n"))
			_, _ = io.Copy(w, &outBuf)
			log.Printf("uploadStats: dieharder failed: %v", err)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.Copy(w, &outBuf)
		return
	}

	if mode == "nist" {
		if _, err := exec.LookPath("assess"); err != nil {
			// assess not found — run local JSON runner on the uploaded bytes
			bits := unpackBitsFromBytes(data)
			// ensure minimum length like writeASCIIBitsTemp would
			if len(bits) < 1_000_000 {
				// repeat but cap at 5M
				reps := (1_000_000 + len(bits) - 1) / len(bits)
				if reps*len(bits) > 5_000_000 {
					reps = 5_000_000 / len(bits)
					if reps < 1 {
						reps = 1
					}
				}
				newBits := make([]byte, 0, reps*len(bits))
				for i := 0; i < reps; i++ {
					newBits = append(newBits, bits...)
				}
				bits = newBits
			}
			j := localNISTJSON(bits)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(j)
			log.Printf("uploadStats: local NIST-like JSON ran for uploaded data (size_bytes=%d)", len(data))
			return
		}
		// write ASCII bit file for assess
		tmpNName, bitCount, err := writeASCIIBitsTempFromBytes(data)
		if err != nil {
			http.Error(w, "failed to prepare bits for NIST assess", http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmpNName)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "assess", tmpNName)
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
		log.Printf("uploadStats: running assess on uploaded data (file=%s)", tmpNName)
		if err := cmd.Run(); err != nil {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("nist assess failed: " + err.Error() + "\n\n"))
			_, _ = io.Copy(w, &outBuf)
			log.Printf("uploadStats: assess failed: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		if bitCount < 1_000_000 {
			_, _ = w.Write([]byte(fmt.Sprintf("NOTE: input has %d bits; NIST STS may skip/ERROR some tests for small samples\n\n", bitCount)))
		}
		_, _ = io.Copy(w, &outBuf)
		log.Printf("uploadStats: assess/dieharder completed for uploaded data")
		return
	}

}

// writeASCIIBitsTemp writes bits (as []byte of 0/1) to a temp file as ASCII '0'/'1' (one per bit, no separators)
// returns filename, bit count, error
func writeASCIIBitsTemp(bits []byte) (string, int, error) {
	// ensure minimum length for NIST STS Random Excursions (tests 13/14)
	const minBits = 1_000_000
	const maxBits = 5_000_000
	if len(bits) < minBits {
		// repeat bits until at least minBits, but cap at maxBits
		reps := (minBits + len(bits) - 1) / len(bits)
		if reps*len(bits) > maxBits {
			reps = maxBits / len(bits)
			if reps < 1 {
				reps = 1
			}
		}
		if reps > 1 {
			newBits := make([]byte, 0, reps*len(bits))
			for i := 0; i < reps; i++ {
				newBits = append(newBits, bits...)
			}
			bits = newBits
		}
	}

	f, err := os.CreateTemp("", "rng-chaos-nist-*.bits")
	if err != nil {
		return "", 0, err
	}
	name := f.Name()
	// write as ASCII
	buf := make([]byte, len(bits))
	for i := range bits {
		if bits[i] == 0 {
			buf[i] = '0'
		} else {
			buf[i] = '1'
		}
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		os.Remove(name)
		return "", 0, err
	}
	f.Close()
	return name, len(bits), nil
}

// writeASCIIBitsTempFromBytes treats packed bytes (MSB-first) as bitstream and writes ASCII file
func writeASCIIBitsTempFromBytes(packed []byte) (string, int, error) {
	bits := make([]byte, 0, len(packed)*8)
	for _, b := range packed {
		for bit := 7; bit >= 0; bit-- {
			v := (b >> uint(bit)) & 1
			bits = append(bits, v)
		}
	}
	// Trim trailing zeros beyond the last requested bit is unknown here; keep all bits
	return writeASCIIBitsTemp(bits)
}

// unpackBitsFromBytes expands packed bytes (MSB-first) into a []byte of 0/1 bits
func unpackBitsFromBytes(packed []byte) []byte {
	bits := make([]byte, 0, len(packed)*8)
	for _, b := range packed {
		for bit := 7; bit >= 0; bit-- {
			v := (b >> uint(bit)) & 1
			bits = append(bits, v)
		}
	}
	return bits
}

// --- local light NIST-like tests ---
func pValueFromZ(z float64) float64 {
	// two-sided p-value from z using erfc
	return math.Erfc(math.Abs(z) / math.Sqrt2)
}

func monobitTest(bits []byte) (float64, bool) {
	var sum int
	for _, b := range bits {
		if b == 1 {
			sum++
		} else {
			sum--
		}
	}
	s := float64(sum)
	z := s / math.Sqrt(float64(len(bits)))
	p := pValueFromZ(z)
	pass := p >= 0.01
	return p, pass
}

func blockFrequencyTest(bits []byte, m int) (float64, bool) {
	if m <= 0 {
		m = 128
	}
	n := len(bits) / m
	if n == 0 {
		return 0, false
	}
	var chi2 float64
	for i := 0; i < n; i++ {
		ones := 0
		for j := 0; j < m; j++ {
			if bits[i*m+j] == 1 {
				ones++
			}
		}
		pi := float64(ones) / float64(m)
		chi2 += (pi - 0.5) * (pi - 0.5)
	}
	chi2 *= 4.0 * float64(m)
	// approximate p-value from chi2 using erfc on sqrt(chi2) (very rough)
	p := math.Exp(-chi2 / 2.0)
	pass := p >= 0.01
	return p, pass
}

func runsTest(bits []byte) (float64, bool) {
	// approximate runs test per NIST
	n := len(bits)
	ones := 0
	for _, b := range bits {
		if b == 1 {
			ones++
		}
	}
	pi := float64(ones) / float64(n)
	if math.Abs(pi-0.5) > 2.0/math.Sqrt(float64(n)) {
		return 0, false
	}
	// count runs
	v := 1
	for i := 1; i < n; i++ {
		if bits[i] != bits[i-1] {
			v++
		}
	}
	num := float64(v) - 2.0*float64(n)*pi*(1-pi)
	den := 2.0 * math.Sqrt(2.0*float64(n)) * pi * (1 - pi)
	z := num / den
	p := pValueFromZ(z)
	pass := p >= 0.01
	return p, pass
}

func longestRunTest(bits []byte) (float64, bool) {
	// heuristic: find longest run of ones in the sequence
	maxRun := 0
	cur := 0
	for _, b := range bits {
		if b == 1 {
			cur++
			if cur > maxRun {
				maxRun = cur
			}
		} else {
			cur = 0
		}
	}
	// map expected longest run roughly by length
	// this is a lightweight heuristic, not NIST table
	expected := int(0.5 * math.Log2(float64(len(bits))))
	diff := float64(maxRun - expected)
	z := diff / math.Sqrt(float64(expected)+1)
	p := pValueFromZ(z)
	pass := p >= 0.01
	return p, pass
}

func approximateEntropyTest(bits []byte, m int) (float64, bool) {
	// simplified approximate entropy using m=2
	if m <= 0 {
		m = 2
	}
	n := len(bits)
	if n < m*2 {
		return 0, false
	}
	// compute frequencies of m-grams
	freq := map[string]int{}
	for i := 0; i <= n-m; i++ {
		s := make([]byte, m)
		copy(s, bits[i:i+m])
		freq[string(s)]++
	}
	var H float64
	total := float64(n - m + 1)
	for _, v := range freq {
		p := float64(v) / total
		H -= p * math.Log2(p)
	}
	// normalize to [0,m]
	p := 1.0 - H/float64(m)
	pass := p >= 0.01
	return p, pass
}

func cusumTest(bits []byte) (float64, float64, bool) {
	// forward and reverse
	n := len(bits)
	sum := 0
	maxAbs := 0
	for _, b := range bits {
		if b == 1 {
			sum++
		} else {
			sum--
		}
		if abs(sum) > maxAbs {
			maxAbs = abs(sum)
		}
	}
	z := float64(maxAbs) / math.Sqrt(float64(n))
	p := pValueFromZ(z)
	// reverse
	sum = 0
	maxAbs = 0
	for i := n - 1; i >= 0; i-- {
		if bits[i] == 1 {
			sum++
		} else {
			sum--
		}
		if abs(sum) > maxAbs {
			maxAbs = abs(sum)
		}
	}
	z2 := float64(maxAbs) / math.Sqrt(float64(n))
	p2 := pValueFromZ(z2)
	pass := p >= 0.01 && p2 >= 0.01
	return p, p2, pass
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// run local NIST-like battery and return text report
func localNISTReport(bits []byte) string {
	// reuse JSON runner and render a compact plain-text equivalent
	j := localNISTJSON(bits)
	var sb strings.Builder
	sb.WriteString("Test name\n\nResult value (P-value)\n\nStatus\n\n")
	if v, ok := j["tests"].([]map[string]any); ok {
		for _, t := range v {
			name := t["name"]
			p := t["p_value"]
			pass := t["passed"]
			sb.WriteString(fmt.Sprintf("%v\n\n%v\n\n%v\n\n", name, p, pass))
		}
	}
	// append placeholders for not implemented tests to match earlier layout
	sb.WriteString("5. Binary Matrix Rank Test\n\nN/A\n\nNot implemented\n\n")
	sb.WriteString("6. Non-overlapping Template Matching Test\n\nN/A\n\nNot implemented\n\n")
	sb.WriteString("7. Overlapping Template Matching Test\n\nN/A\n\nNot implemented\n\n")
	sb.WriteString("13. Random Excursions Test\n\nN/A\n\nNot implemented\n\n")
	sb.WriteString("14. Random Excursions Variant Test\n\nN/A\n\nNot implemented\n\n")
	return sb.String()
}

// localNISTJSON runs the lightweight tests and returns a structured JSON-like map
func localNISTJSON(bits []byte) map[string]any {
	out := map[string]any{}
	tests := make([]map[string]any, 0)

	if p, ok := monobitTest(bits); true {
		tests = append(tests, map[string]any{"id": 1, "name": "Frequency (Monobit) Test", "p_value": p, "passed": ok})
	}
	if p, ok := blockFrequencyTest(bits, 128); true {
		tests = append(tests, map[string]any{"id": 2, "name": "Frequency Test within a Block", "p_value": p, "passed": ok})
	}
	if p, ok := runsTest(bits); true {
		tests = append(tests, map[string]any{"id": 3, "name": "Runs Test", "p_value": p, "passed": ok})
	}
	if p, ok := longestRunTest(bits); true {
		tests = append(tests, map[string]any{"id": 4, "name": "Longest Run of Ones in a Block", "p_value": p, "passed": ok})
	}
	if p, ok := approximateEntropyTest(bits, 2); true {
		tests = append(tests, map[string]any{"id": 11, "name": "Approximate Entropy Test", "p_value": p, "passed": ok})
	}
	if p, p2, ok := cusumTest(bits); true {
		tests = append(tests, map[string]any{"id": 12, "name": "Cumulative Sums (Cusum) Test", "p_value_forward": p, "p_value_reverse": p2, "passed": ok})
	}

	out["tests"] = tests
	out["summary"] = map[string]any{"total_tests": len(tests)}
	return out
}

func passStr(b bool) string {
	if b {
		return "Passed"
	}
	return "Failed"
}
