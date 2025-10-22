package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// deriveSeed: собирает сырьё по EntropySpec и выдаёт мастер-seed (int64) + тег-строку
// Воспроизводимость гарантируется: при mode=repro используем Seed64;
// для прочих режимов возвращаем сгенерированный seed и сохраняем его в транзакции.
func deriveSeed(es EntropySpec) (seed int64, tag string, perSeeds []int64) {
	switch es.Mode {
	case "repro":
		return es.Seed64, "mode:repro seed=" + itoa64(es.Seed64), nil
	case "os":
		return seedFromOS(), "mode:os", nil
	case "jitter":
		return seedFromJitter(32), "mode:jitter", nil
	case "http":
		return seedFromHTTP(es.HTTP), "mode:http:" + hexOfURLs(es.HTTP), nil
	case "mix":
		raw := make([][]byte, 0, 4)
		raw = append(raw, rawFromOS(32))
		raw = append(raw, rawFromJitter(48))
		perSeedsStr := make([]string, 0, len(es.HTTP))
		perSeeds = make([]int64, 0, len(es.HTTP))
		if len(es.HTTP) > 0 {
			// for each URL, fetch independently and include its raw into mix
			for _, u := range es.HTTP {
				b := rawFromHTTP([]string{u})
				raw = append(raw, b)
				var s int64
				if len(b) >= 8 {
					s = int64(binary.LittleEndian.Uint64(b[:8]))
				} else {
					// fallback: mix with OS bytes
					extra := rawFromOS(8)
					buf := append(b, extra...)
					s = int64(binary.LittleEndian.Uint64(buf[:8]))
				}
				perSeeds = append(perSeeds, s)
				perSeedsStr = append(perSeedsStr, itoa64(s))
			}
		}
		h := sha256.New()
		for _, b := range raw {
			h.Write(b)
		}
		sum := h.Sum(nil)
		// derive final seed as SHA256(sum || label) first 8 bytes
		lab := []byte("seed-mix-v1")
		s2 := sha256.Sum256(append(sum, lab...))
		seed = int64(binary.LittleEndian.Uint64(s2[:8]))
		tag = "mode:mix http=" + hexOfURLs(es.HTTP)
		if len(perSeedsStr) > 0 {
			tag += " per_seeds=" + strings.Join(perSeedsStr, ",")
		}
		return seed, tag, perSeeds
	default:
		return seedFromOS(), "mode:mix", nil
	}
}

func seedFromOS() int64 {
	var b [8]byte
	_, _ = io.ReadFull(rand.Reader, b[:])
	return int64(binary.LittleEndian.Uint64(b[:]))
}
func rawFromOS(n int) []byte {
	b := make([]byte, n)
	_, _ = io.ReadFull(rand.Reader, b)
	return b
}

// джиттер: многократно измеряем наносекундные интервалы, мешаем SHA256
func seedFromJitter(rounds int) int64 {
	b := rawFromJitter(rounds)
	var out [8]byte
	copy(out[:], b[:8])
	return int64(binary.LittleEndian.Uint64(out[:]))
}
func rawFromJitter(rounds int) []byte {
	h := sha256.New()
	tmp := make([]byte, 8)
	for i := 0; i < rounds; i++ {
		t0 := time.Now()
		spin := 100 + (i % 17)
		for k := 0; k < spin; k++ { // пустая работа
		}
		time.Sleep(0)
		dt := time.Since(t0).Nanoseconds()
		binary.LittleEndian.PutUint64(tmp, uint64(dt))
		h.Write(tmp)
		binary.LittleEndian.PutUint64(tmp, uint64(time.Now().UnixNano()))
		h.Write(tmp)
	}
	return h.Sum(nil)
}

func seedFromHTTP(urls []string) int64 {
	b := rawFromHTTP(urls)
	if len(b) < 8 {
		b = append(b, rawFromOS(8)...)
	}
	return int64(binary.LittleEndian.Uint64(b[:8]))
}
func rawFromHTTP(urls []string) []byte {
	cli := &http.Client{Timeout: 3 * time.Second}
	h := sha256.New()
	var direct []byte
	var gotDirect bool
	for _, u := range urls {
		if strings.TrimSpace(u) == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := cli.Do(req)
		if err == nil && resp != nil {
			func() {
				defer resp.Body.Close()
				// мешаем статус, заголовки и кусок тела
				// try to read entire small body
				body, _ := io.ReadAll(resp.Body)
				sbody := strings.TrimSpace(string(body))
				// if body is 64-hex chars, use raw bytes directly
				// if body is 64-hex chars, decode it; otherwise take trimmed plain text
				if len(sbody) == 64 {
					if b, err := hex.DecodeString(sbody); err == nil {
						// deterministic: use decoded bytes directly
						direct = make([]byte, len(b))
						copy(direct, b)
						gotDirect = true
						return
					}
				}
				if sbody != "" {
					// if body is a decimal integer, parse it and return its little-endian int64 bytes
					if iv, err := strconv.ParseInt(sbody, 10, 64); err == nil {
						var buf [8]byte
						binary.LittleEndian.PutUint64(buf[:], uint64(iv))
						direct = make([]byte, 8)
						copy(direct, buf[:])
						gotDirect = true
						return
					}
					// otherwise, deterministic: hash the trimmed plain-text (no time salt)
					hh := sha256.Sum256([]byte(sbody))
					h.Write(hh[:])
					return
				}
				// fallback: include some headers/status/URL bytes
				tmp := make([]byte, 512)
				copy(tmp, body)
				h.Write([]byte(u))
				h.Write([]byte(resp.Status))
				for k, vv := range resp.Header {
					h.Write([]byte(k))
					for _, s := range vv {
						h.Write([]byte(s))
					}
				}
				h.Write(tmp)
			}()
			if gotDirect {
				cancel()
				return direct
			}
		}
		cancel()
	}
	return h.Sum(nil)
}

func hexOfURLs(v []string) string {
	h := sha256.New()
	for _, s := range v {
		h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func itoa64(v int64) string {
	sign := ""
	u := uint64(v)
	if v < 0 {
		sign = "-"
		u = uint64(-v)
	}
	if u == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for u > 0 {
		buf = append(buf, byte('0'+u%10))
		u /= 10
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return sign + string(buf)
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
