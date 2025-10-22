package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ======= helpers =======
func atoi(q string, def int) int {
	if q == "" {
		return def
	}
	v, err := strconv.Atoi(q)
	if err != nil {
		return def
	}
	return v
}

// utility to build a new URL with same path and provided query values
func newURLWithQuery(path string, q url.Values) *url.URL {
	return &url.URL{Path: path, RawQuery: q.Encode()}
}
func atof(q string, def float64) float64 {
	if q == "" {
		return def
	}
	v, err := strconv.ParseFloat(q, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return def
	}
	return v
}

// ======= handlers =======
func generateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	// параметры хаоса/отрисовки
	gp := GenerateParams{
		Count:      atoi(q.Get("count"), 1_000_000),
		CanvasW:    atoi(q.Get("w"), 1024),
		CanvasH:    atoi(q.Get("h"), 1024),
		Iterations: atoi(q.Get("iter"), 8_000),
		NumPoints:  atoi(q.Get("points"), 20),
		PixelWidth: atoi(q.Get("px"), 4),
		Step:       atof(q.Get("step"), 0.01),
		Motion: MotionSpec{
			Law:        strings.ToLower(q.Get("law")),
			Sharpness:  atof(q.Get("sharp"), 1.0),
			Smoothness: atof(q.Get("smooth"), 1.0),
			SpeedScale: atof(q.Get("speed"), 1.0),
		},
		Entropy: EntropySpec{
			Mode:   strings.ToLower(q.Get("entropy")),
			Seed64: 0,
			HTTP:   []string{"https://87ct9p48-8000.euw.devtunnels.ms/last_seed"},
		},
		Whiten: strings.ToLower(q.Get("whiten")),
	}
	if gp.Motion.Law == "" {
		gp.Motion.Law = "flow"
	}
	if gp.Entropy.Mode == "" {
		gp.Entropy.Mode = "mix"
	}
	if seedStr := q.Get("seed"); seedStr != "" {
		// для repro
		if s, err := strconv.ParseInt(seedStr, 10, 64); err == nil {
			gp.Entropy.Seed64 = s
			gp.Entropy.Mode = "repro"
		}
	}
	if httpList := q.Get("http"); httpList != "" {
		gp.Entropy.HTTP = strings.Split(httpList, ",")
	}

	// Per-parameter defaults (use the user's standard values when specific param missing)
	// Count already defaults to 1_000_000 in constructor above.
	if q.Get("iter") == "" {
		gp.Iterations = 6000
	}
	if q.Get("points") == "" {
		gp.NumPoints = 20
	}
	if q.Get("law") == "" {
		gp.Motion.Law = "random"
	}
	if q.Get("entropy") == "" {
		gp.Entropy.Mode = "mix"
	}
	// leave gp.Entropy.HTTP as set in constructor when no http query param is provided
	if q.Get("whiten") == "" {
		gp.Whiten = "hybrid"
	}

	log.Printf("generate: starting generation (count=%d, whiten=%s, law=%s, entropy=%s)", gp.Count, gp.Whiten, gp.Motion.Law, gp.Entropy.Mode)
	// 1) получаем мастер-seed
	seed, entropyTag, perSeeds := deriveSeed(gp.Entropy)
	log.Printf("generate: derived seed=%d tag=%s", seed, entropyTag)

	// 2) запускаем симуляцию
	log.Printf("generate: starting simulation for tx (seed=%d) iterations=%d points=%d", seed, gp.Iterations, gp.NumPoints)
	sim, digest := runSimulation(seed, gp)
	log.Printf("generate: simulation complete for seed=%d", seed)

	// 3) из digest разворачиваем итоговые биты (с режимом whitening)
	log.Printf("generate: expanding bits (count=%d, whiten=%s)", gp.Count, gp.Whiten)
	bits := expandBitsFromPathDigest(digest, gp.Count, gp.Whiten)
	log.Printf("generate: expanded bits (requested=%d, obtained=%d)", gp.Count, len(bits))

	// 4) data hash: use path digest (already computed) to avoid expensive JSON marshaling of full simulation
	dh := sha256.Sum256(digest[:])
	hbits := sha256.New()
	for i := range bits {
		if bits[i] != 0 {
			hbits.Write([]byte{1})
		} else {
			hbits.Write([]byte{0})
		}
	}
	bitsSum := hbits.Sum(nil)
	bitsHash := hex.EncodeToString(bitsSum)

	// derive published = SHA256(bitsSum || dh || label)
	lbl := []byte("published-hash-v2")
	tmp := make([]byte, 0, len(bitsSum)+len(dh[:])+len(lbl))
	tmp = append(tmp, bitsSum...)
	tmp = append(tmp, dh[:]...)
	tmp = append(tmp, lbl...)
	publishedSum := sha256.Sum256(tmp)
	var published [32]byte
	copy(published[:], publishedSum[:])

	// 5) формируем транзакцию
	tx := &Transaction{
		TxID:      newUUID(),
		CreatedAt: time.Now().UTC(),
		Count:     gp.Count,
		Seed:      seed,
		Sim:       sim,
		DataHash:  hex.EncodeToString(dh[:]),
		BitsHash:  bitsHash,
		Published: hex.EncodeToString(published[:]),
		Provenance: GenerationProvenance{
			Entropy:      gp.Entropy,
			Motion:       gp.Motion,
			Iterations:   gp.Iterations,
			NumPoints:    gp.NumPoints,
			PixelWidth:   gp.PixelWidth,
			CanvasW:      gp.CanvasW,
			CanvasH:      gp.CanvasH,
			Step:         gp.Step,
			Whiten:       gp.Whiten,
			PerHTTPSeeds: perSeeds,
		},
	}
	// добавим тег выбранного источника (удобно видеть в /info)
	tx.Provenance.Entropy.Mode = entropyTag

	// 6) сохраняем
	txMutex.Lock()
	txStore[tx.TxID] = tx
	txMutex.Unlock()
	appendBlock(tx)
	log.Printf("generate: created tx %s seed=%d count=%d", tx.TxID, seed, gp.Count)

	// 7) ответ
	resp := map[string]any{
		"tx_id":      tx.TxID,
		"created_at": tx.CreatedAt.Format(time.RFC3339),
		"seed":       seed,
		"count":      gp.Count,
		"replay_hint": map[string]any{
			"entropy_mode": gp.Entropy.Mode,
			"seed":         seed, // достаточно для воспроизведения
			"law":          gp.Motion.Law,
			"sharp":        gp.Motion.Sharpness,
			"smooth":       gp.Motion.Smoothness,
			"speed":        gp.Motion.SpeedScale,
			"iter":         gp.Iterations,
			"points":       gp.NumPoints,
			"w":            gp.CanvasW,
			"h":            gp.CanvasH,
			"px":           gp.PixelWidth,
			"step":         gp.Step,
			"whiten":       gp.Whiten,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func chainHandler(w http.ResponseWriter, r *http.Request) {
	chainMutex.RLock()
	defer chainMutex.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chain)
}

// /tx/{id}/png  /json  /txt  /bin  /verify  /info  /reproduce
func txRouter(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/tx/")
	parts := strings.SplitN(p, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing tx id", http.StatusBadRequest)
		return
	}
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	switch action {
	case "png":
		txPNG(w, r, id)
	case "json":
		txJSON(w, r, id)
	case "txt":
		txTXT(w, r, id)
	case "bin":
		txBIN(w, r, id)
	case "verify":
		txVerify(w, r, id)
	case "info":
		txInfo(w, r, id)
	case "reproduce":
		txReproduce(w, r, id)
	case "trng":
		txTRNG(w, r, id)
	case "stats":
		txStats(w, r, id)
	default:
		log.Printf("txRouter: unknown action '%s' for tx %s", action, id)
		http.Error(w, "unknown tx action", http.StatusNotFound)
	}
}

func txPNG(w http.ResponseWriter, r *http.Request, id string) {
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	w.Header().Set("Content-Type", "image/png")
	if err := writePNG(w, tx.Sim); err != nil {
		log.Printf("png err: %v", err)
	}
}
func txJSON(w http.ResponseWriter, r *http.Request, id string) {
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tx.Sim)
}
func txTXT(w http.ResponseWriter, r *http.Request, id string) {
	// Alias to /tx/{id}/trng?format=bin&type=txt
	// We simply construct a new request-like URL with the desired query params and call txTRNG
	q := r.URL.Query()
	q.Set("format", "bin")
	q.Set("type", "txt")
	// replace r.URL.RawQuery for downstream handler
	r2 := *r
	r2.URL = newURLWithQuery(r.URL.Path, q)
	txTRNG(w, &r2, id)
}

func txBIN(w http.ResponseWriter, r *http.Request, id string) {
	// Alias to /tx/{id}/trng?format=raw&type=bin
	q := r.URL.Query()
	q.Set("format", "raw")
	q.Set("type", "bin")
	r2 := *r
	r2.URL = newURLWithQuery(r.URL.Path, q)
	txTRNG(w, &r2, id)
}
func txVerify(w http.ResponseWriter, r *http.Request, id string) {
	ok := validateChain()
	tx := mustTx(id, w)
	if tx == nil {
		return
	}

	// пересчёт dataHash и bitsHash
	// Важно: при генерации DataHash вычислялся как SHA256(pathDigest)
	// (см. generateHandler). Ранее здесь ошибочно хешировался JSON симуляции,
	// что приводило к постоянному несоответствию. Считаем так же, как при генерации.
	gp := paramsFromTx(tx)
	_, digest := runSimulation(tx.Seed, gp)
	// dh2 должен быть SHA256 от path-digest, чтобы совпадать с tx.DataHash
	dh2 := sha256.Sum256(digest[:])
	bits := expandBitsFromPathDigest(digest, tx.Count, gp.Whiten)
	hb := sha256.New()
	for _, b := range bits {
		if b != 0 {
			hb.Write([]byte{1})
		} else {
			hb.Write([]byte{0})
		}
	}
	bits2 := hex.EncodeToString(hb.Sum(nil))

	resp := map[string]any{
		"chain_valid":        ok,
		"tx_found":           true,
		"data_hash_match":    hex.EncodeToString(dh2[:]) == tx.DataHash,
		"bits_hash_match":    bits2 == tx.BitsHash,
		"published_in_chain": false,
	}
	// проверим в блоке
	chainMutex.RLock()
	for i := range chain {
		if chain[i].TxID == id {
			resp["published_in_chain"] = (chain[i].DataHash == tx.Published)
			break
		}
	}
	chainMutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
func txInfo(w http.ResponseWriter, r *http.Request, id string) {
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	log.Printf("txInfo: serving info for tx %s", id)
	// include replay URL
	gp := tx.Provenance
	// build query params for reproduction
	q := make([]string, 0)
	q = append(q, fmt.Sprintf("entropy=%s", gp.Entropy.Mode))
	q = append(q, fmt.Sprintf("seed=%d", tx.Seed))
	if len(gp.Entropy.HTTP) > 0 {
		q = append(q, fmt.Sprintf("http=%s", strings.Join(gp.Entropy.HTTP, ",")))
	}
	q = append(q, fmt.Sprintf("law=%s", gp.Motion.Law))
	q = append(q, fmt.Sprintf("iter=%d", gp.Iterations))
	q = append(q, fmt.Sprintf("points=%d", gp.NumPoints))
	q = append(q, fmt.Sprintf("w=%d", gp.CanvasW))
	q = append(q, fmt.Sprintf("h=%d", gp.CanvasH))
	q = append(q, fmt.Sprintf("px=%d", gp.PixelWidth))
	q = append(q, fmt.Sprintf("step=%g", gp.Step))
	q = append(q, fmt.Sprintf("whiten=%s", gp.Whiten))
	replayURL := "/generate?" + strings.Join(q, "&")

	out := map[string]any{
		"tx":         tx,
		"replay_url": replayURL,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// /txs - return a list of transaction summaries (omit large SimulationData)
func txsHandler(w http.ResponseWriter, r *http.Request) {
	// return list of transactions (snapshot under lock)
	txMutex.RLock()
	list := make([]map[string]any, 0, len(txStore))
	for _, tx := range txStore {
		item := map[string]any{
			"tx_id":      tx.TxID,
			"created_at": tx.CreatedAt.Format(time.RFC3339),
			"count":      tx.Count,
			"seed":       tx.Seed,
			"data_hash":  tx.DataHash,
			"bits_hash":  tx.BitsHash,
			"published":  tx.Published,
			"provenance": tx.Provenance,
		}
		list = append(list, item)
	}
	txMutex.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}
func txReproduce(w http.ResponseWriter, r *http.Request, id string) {
	// отдаём те же данные, что и /tx/{id}/txt, но с другим именем
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	gp := paramsFromTx(tx)
	_, digest := runSimulation(tx.Seed, gp)
	bits := expandBitsFromPathDigest(digest, tx.Count, gp.Whiten)
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.reproduce.txt\"", id))
	for _, b := range bits {
		if b == 0 {
			w.Write([]byte{'0'})
		} else {
			w.Write([]byte{'1'})
		}
	}
}

// /tx/{id}/trng?n=64&format=hex|bin|raw&type=txt|bin
// - format=hex  : default, returns hex string of the raw bytes
// - format=raw  : returns raw bytes (previously 'bin')
// - format=bin  : returns textual bitstring like "101010..." (MSB-first per byte)
// type controls Content-Disposition/filename extension: txt => .txt (text/plain), bin => .bin (application/octet-stream)
func txTRNG(w http.ResponseWriter, r *http.Request, id string) {
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	q := r.URL.Query()
	// n is number of bits; default to tx.Count (stored in bits)
	nBits := atoi(q.Get("n"), -1)
	if nBits <= 0 {
		nBits = tx.Count
	}
	format := strings.ToLower(q.Get("format"))
	if format == "" {
		format = "hex"
	}
	typ := strings.ToLower(q.Get("type"))

	tr := NewTRNGFromTx(tx)
	needed := (nBits + 7) / 8
	data := tr.ReadBytes(needed)

	// zero out unused LSBs in last byte so packed output contains exactly nBits
	if nBits%8 != 0 {
		keep := uint(nBits % 8)
		mask := byte(0xFF) << (8 - keep)
		data[needed-1] &= mask
	}

	// prepare Content-Type and Content-Disposition based on type param
	var contentType string
	var ext string
	switch typ {
	case "bin":
		contentType = "application/octet-stream"
		ext = "bin"
	case "txt":
		contentType = "text/plain"
		ext = "txt"
	default:
		if format == "raw" || format == "bytes" {
			contentType = "application/octet-stream"
			ext = "bin"
		} else {
			contentType = "text/plain"
			ext = "txt"
		}
	}

	// handle raw bytes
	if format == "raw" || format == "bytes" {
		w.Header().Set("Content-Type", contentType)
		if ext != "" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.%s\"", id, ext))
		}
		_, _ = w.Write(data)
		return
	}

	// handle textual bit string (MSB-first), exactly nBits characters
	if format == "bin" {
		out := make([]byte, 0, nBits)
		written := 0
		for i := 0; i < len(data) && written < nBits; i++ {
			b := data[i]
			for bit := 7; bit >= 0 && written < nBits; bit-- {
				if (b>>uint(bit))&1 == 1 {
					out = append(out, '1')
				} else {
					out = append(out, '0')
				}
				written++
			}
		}
		w.Header().Set("Content-Type", contentType)
		if ext != "" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.%s\"", id, ext))
		}
		_, _ = w.Write(out)
		return
	}

	// default: hex of packed bytes covering nBits
	w.Header().Set("Content-Type", contentType)
	if ext != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.%s\"", id, ext))
	}
	_, _ = w.Write([]byte(hex.EncodeToString(data)))
}

func mustTx(id string, w http.ResponseWriter) *Transaction {
	txMutex.RLock()
	tx, ok := txStore[id]
	txMutex.RUnlock()
	if !ok {
		http.Error(w, "tx not found", http.StatusNotFound)
		return nil
	}
	return tx
}

func paramsFromTx(tx *Transaction) GenerateParams {
	return GenerateParams{
		Count:      tx.Count,
		CanvasW:    tx.Provenance.CanvasW,
		CanvasH:    tx.Provenance.CanvasH,
		Iterations: tx.Provenance.Iterations,
		NumPoints:  tx.Provenance.NumPoints,
		PixelWidth: tx.Provenance.PixelWidth,
		Step:       tx.Provenance.Step,
		Entropy:    tx.Provenance.Entropy,
		Motion:     tx.Provenance.Motion,
		Whiten:     tx.Provenance.Whiten,
	}
}
