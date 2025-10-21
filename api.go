package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
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
		gp.Iterations = 8000
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
	if q.Get("http") == "" {
		// default HTTP seed source when none supplied
		gp.Entropy.HTTP = []string{""}
	}
	if q.Get("whiten") == "" {
		gp.Whiten = "aes"
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
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	// регенерируем биты детерминированно из pathDigest → для этого пересобираем digest
	gp := paramsFromTx(tx)
	_, digest := runSimulation(tx.Seed, gp)
	bits := expandBitsFromPathDigest(digest, tx.Count, gp.Whiten)

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.txt\"", id))
	for _, b := range bits {
		if b == 0 {
			_, _ = w.Write([]byte{'0'})
		} else {
			_, _ = w.Write([]byte{'1'})
		}
	}
}

func txBIN(w http.ResponseWriter, r *http.Request, id string) {
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	gp := paramsFromTx(tx)
	_, digest := runSimulation(tx.Seed, gp)
	bits := expandBitsFromPathDigest(digest, tx.Count, gp.Whiten)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.bin\"", id))
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
	_, _ = w.Write(out)
}
func txVerify(w http.ResponseWriter, r *http.Request, id string) {
	ok := validateChain()
	tx := mustTx(id, w)
	if tx == nil {
		return
	}

	// пересчёт dataHash и bitsHash
	gp := paramsFromTx(tx)
	sim2, digest := runSimulation(tx.Seed, gp)
	dataBytes, _ := json.Marshal(sim2)
	dh2 := sha256.Sum256(dataBytes)
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

// /tx/{id}/trng?n=64&format=bin|hex
func txTRNG(w http.ResponseWriter, r *http.Request, id string) {
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	q := r.URL.Query()
	n := atoi(q.Get("n"), 64)
	if n <= 0 {
		n = 64
	}
	format := strings.ToLower(q.Get("format"))
	if format == "" {
		format = "hex"
	}

	tr := NewTRNGFromTx(tx)
	data := tr.ReadBytes(n)

	if format == "bin" {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
		return
	}
	// default: hex
	w.Header().Set("Content-Type", "text/plain")
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
