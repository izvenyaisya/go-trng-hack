package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"math"
	mrand "math/rand"
	"strings"
)

type mover struct {
	x, y   float64
	vx, vy float64
	color  string
	path   []XY
}

func (m *mover) record() { m.path = append(m.path, XY{m.x, m.y}) }

// runSimulation: полностью детерминирована master-seed'ом и GenerateParams
// Возвращает симуляцию и внутренний агрегированный хэш траектории (pathDigest)
func runSimulation(seed int64, gp GenerateParams) (SimulationData, [32]byte) {
	rnd := mrand.New(mrand.NewSource(seed))

	// инициализация точек
	colors := defaultColors()
	pts := make([]*mover, 0, gp.NumPoints)
	for i := 0; i < gp.NumPoints; i++ {
		pts = append(pts, &mover{
			x:     rnd.Float64() * float64(gp.CanvasW),
			y:     rnd.Float64() * float64(gp.CanvasH),
			vx:    (rnd.Float64()*2 - 1) * (2 + gp.Motion.SpeedScale*2),
			vy:    (rnd.Float64()*2 - 1) * (2 + gp.Motion.SpeedScale*2),
			color: colors[i%len(colors)],
			path:  make([]XY, 0, gp.Iterations),
		})
	}

	// подготовка шума
	// golden ratio-derived scramble constant as uint64; XOR via uint64 to avoid overflow
	const scramble uint64 = 0x9e3779b97f4a7c15
	n := newSimpleNoise(int64(uint64(seed) ^ scramble))

	// движение
	step := gp.Step
	if step <= 0 {
		step = 0.01
	}
	sharp := clamp(gp.Motion.Sharpness, 0, 2)
	smooth := clamp(gp.Motion.Smoothness, 0, 2)
	maxV := 20.0 * (0.5 + gp.Motion.SpeedScale)

	h := sha256.New()

	// prepare law choices: single law, comma-separated list, or "random" => all supported
	var lawChoices []string
	lawParam := strings.ToLower(gp.Motion.Law)
	if lawParam == "random" || lawParam == "rand" {
		lawChoices = []string{"flow", "sine", "jerk", "spiral"}
	} else if strings.Contains(lawParam, ",") {
		parts := strings.Split(lawParam, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
			if parts[i] == "" {
				parts = append(parts[:i], parts[i+1:]...)
			}
		}
		if len(parts) == 0 {
			parts = []string{"flow"}
		}
		lawChoices = parts
	} else {
		lawChoices = []string{lawParam}
	}

	for t := 0; t < gp.Iterations; t++ {
		timeOff := float64(t) * step
		// choose law for this tick (deterministically via rnd)
		law := lawChoices[0]
		if len(lawChoices) > 1 {
			law = lawChoices[rnd.Intn(len(lawChoices))]
		}
		for _, p := range pts {
			ax, ay := 0.0, 0.0
			switch law {
			case "sine":
				ax += math.Sin(timeOff+p.x*0.01) * (0.5 + smooth)
				ay += math.Cos(timeOff+p.y*0.01) * (0.5 + smooth)
			case "jerk":
				// редкие импульсы
				if rnd.Float64() < 0.02*(0.5+sharp) {
					ang := rnd.Float64() * 2 * math.Pi
					imp := 6.0 * (0.5 + sharp)
					ax += imp * math.Cos(ang)
					ay += imp * math.Sin(ang)
				}
			case "spiral":
				cx, cy := float64(gp.CanvasW)/2, float64(gp.CanvasH)/2
				dx, dy := cx-p.x, cy-p.y
				ang := math.Atan2(dy, dx)
				rad := (0.8 + smooth) * 1.2
				tan := (0.5 + sharp) * 1.2
				ax += rad*math.Cos(ang) - tan*math.Sin(ang)
				ay += rad*math.Sin(ang) + tan*math.Cos(ang)
			default: // flow / perlin-подобный
				ax += n.noise2d(p.x*0.006+timeOff, p.y*0.006) * (1.0 + smooth)
				ay += n.noise2d(p.y*0.006-timeOff, p.x*0.006) * (1.0 + smooth)
			}

			// апдейт скорости/позиции
			p.vx = (p.vx + ax*(0.2+0.2*sharp)) * (0.98 + 0.01*smooth)
			p.vy = (p.vy + ay*(0.2+0.2*sharp)) * (0.98 + 0.01*smooth)

			vmag := math.Hypot(p.vx, p.vy)
			if vmag > maxV {
				scale := maxV / vmag
				p.vx *= scale
				p.vy *= scale
			}

			p.x += p.vx
			p.y += p.vy

			// отражение от границ
			if p.x < 0 {
				p.x = 0
				p.vx = -p.vx
			}
			if p.y < 0 {
				p.y = 0
				p.vy = -p.vy
			}
			if p.x > float64(gp.CanvasW) {
				p.x = float64(gp.CanvasW)
				p.vx = -p.vx
			}
			if p.y > float64(gp.CanvasH) {
				p.y = float64(gp.CanvasH)
				p.vy = -p.vy
			}

			p.record()

			// include full float64 bits for more entropy in path digest
			var tmp [16]byte
			binary.LittleEndian.PutUint64(tmp[:8], mathFloat64bits(p.x))
			binary.LittleEndian.PutUint64(tmp[8:], mathFloat64bits(p.y))
			h.Write(tmp[:])
		}
	}

	sim := SimulationData{
		CanvasWidth:   gp.CanvasW,
		CanvasHeight:  gp.CanvasH,
		Iterations:    gp.Iterations,
		Points:        make([]Point, 0, len(pts)),
		Step:          step,
		MotionLaw:     gp.Motion.Law,
		Sharpness:     sharp,
		Smoothness:    smooth,
		SpeedScale:    gp.Motion.SpeedScale,
		EntropyMode:   gp.Entropy.Mode,
		NumPoints:     gp.NumPoints,
		DefaultColors: true,
	}
	for i, p := range pts {
		sim.Points = append(sim.Points, Point{
			ID:         i,
			Color:      p.color,
			PixelWidth: gp.PixelWidth,
			Path:       p.path,
		})
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return sim, digest
}

// извлекаем биты из pathDigest детерминированно: H(digest||ctr)
// Whiten modes: "off" (default), "on" (simple xorshift-LFSR whitening), "hmac" (HMAC-SHA256-CTR PRF)
func expandBitsFromPathDigest(digest [32]byte, outBits int, mode string) []byte {
	out := make([]byte, outBits)
	used := 0
	if mode == "hmac" {
		// seed HMAC-DRBG once with the digest (legacy)
		drbg := newHMACDRBG(digest[:])
		needed := (outBits + 7) / 8
		buf := drbg.Generate(needed)
		for _, b := range buf {
			for bit := 7; bit >= 0 && used < outBits; bit-- {
				out[used] = (b >> uint(bit)) & 1
				used++
			}
			if used >= outBits {
				break
			}
		}
		return out
	}

	// AES-CTR based mode: derive AES-256 key and IV from digest and stream out bytes
	if mode == "aes" {
		// derive key = SHA256(digest||"aes-ctr-key-v1")
		kInput := make([]byte, 0, len(digest)+16)
		kInput = append(kInput, digest[:]...)
		kInput = append(kInput, []byte("aes-ctr-key-v1")...)
		keySum := sha256.Sum256(kInput)

		// derive iv = first 16 bytes of SHA256(digest||"aes-ctr-iv-v1")
		ivInput := make([]byte, 0, len(digest)+16)
		ivInput = append(ivInput, digest[:]...)
		ivInput = append(ivInput, []byte("aes-ctr-iv-v1")...)
		ivSum := sha256.Sum256(ivInput)

		block, err := aes.NewCipher(keySum[:])
		if err != nil {
			// fallback to simple SHA-based expansion on failure
			// (shouldn't happen with valid key length)
			var ctr uint64 = 0
			for used < outBits {
				var c [8]byte
				binary.LittleEndian.PutUint64(c[:], ctr)
				h := sha256.New()
				h.Write(digest[:])
				h.Write([]byte("chaos-expand-v2"))
				h.Write(c[:])
				blockb := h.Sum(nil)
				for _, b := range blockb {
					for bit := 7; bit >= 0 && used < outBits; bit-- {
						out[used] = (b >> uint(bit)) & 1
						used++
					}
					if used >= outBits {
						break
					}
				}
				ctr++
			}
			return out
		}

		// prepare CTR stream
		ctr := make([]byte, aes.BlockSize)
		copy(ctr, ivSum[:aes.BlockSize])
		stream := cipher.NewCTR(block, ctr)
		needed := (outBits + 7) / 8
		buf := make([]byte, needed)
		stream.XORKeyStream(buf, buf) // XOR with zero => just fills buf with keystream
		for _, b := range buf {
			for bit := 7; bit >= 0 && used < outBits; bit-- {
				out[used] = (b >> uint(bit)) & 1
				used++
			}
			if used >= outBits {
				break
			}
		}
		return out
	}

	var ctr uint64 = 0
	for used < outBits {
		// prepare per-block seed
		var c [8]byte
		binary.LittleEndian.PutUint64(c[:], ctr)

		// generate a block of pseudorandom bytes depending on mode
		var block []byte
		switch mode {
		case "on":
			// simple derivation then LFSR-based whitening of that block
			h := sha256.New()
			h.Write(digest[:])
			h.Write([]byte("chaos-expand-v1"))
			h.Write(c[:])
			block = h.Sum(nil)
			// apply small xorshift LFSR to block bytes
			lfsr := uint32(binary.LittleEndian.Uint32(block[:4]) ^ 0xA5A5A5A5)
			for i := range block {
				// xorshift32
				lfsr ^= lfsr << 13
				lfsr ^= lfsr >> 17
				lfsr ^= lfsr << 5
				block[i] ^= byte(lfsr & 0xFF)
			}
		default:
			h := sha256.New()
			h.Write(digest[:])
			h.Write([]byte("chaos-expand-v1"))
			h.Write(c[:])
			block = h.Sum(nil)
		}

		// extract bits MSB-first from block
		for _, b := range block {
			for bit := 7; bit >= 0 && used < outBits; bit-- {
				out[used] = (b >> uint(bit)) & 1
				used++
			}
			if used >= outBits {
				break
			}
		}
		ctr++
	}
	return out
}

// small helper: HMAC-SHA256 using digest as key
func hmacSHA256(key, msg []byte) []byte {
	// simple HMAC implementation
	blockSize := 64
	if len(key) > blockSize {
		h := sha256.Sum256(key)
		key = h[:]
	}
	if len(key) < blockSize {
		tmp := make([]byte, blockSize)
		copy(tmp, key)
		key = tmp
	}
	okey := make([]byte, blockSize)
	ikey := make([]byte, blockSize)
	for i := 0; i < blockSize; i++ {
		okey[i] = key[i] ^ 0x5c
		ikey[i] = key[i] ^ 0x36
	}
	h1 := sha256.New()
	h1.Write(ikey)
	h1.Write(msg)
	inner := h1.Sum(nil)
	h2 := sha256.New()
	h2.Write(okey)
	h2.Write(inner)
	return h2.Sum(nil)
}

// --- простейший value noise ---
type simpleNoise struct{ seed int64 }

func newSimpleNoise(seed int64) *simpleNoise { return &simpleNoise{seed: seed} }
func (s *simpleNoise) hashInt(x, y int64) int64 {
	h := x*374761393 + y*668265263 + s.seed*1274126177
	h = (h ^ (h >> 13)) * 1274126177
	return h
}
func (s *simpleNoise) val(ix, iy int64) float64 {
	v := s.hashInt(ix, iy)
	return float64(v&0x7fffffff)/float64(0x7fffffff)*2 - 1
}
func (s *simpleNoise) noise2d(x, y float64) float64 {
	xi := int64(math.Floor(x))
	yi := int64(math.Floor(y))
	tx := x - float64(xi)
	ty := y - float64(yi)
	v00 := s.val(xi, yi)
	v10 := s.val(xi+1, yi)
	v01 := s.val(xi, yi+1)
	v11 := s.val(xi+1, yi+1)
	sx := tx * tx * (3 - 2*tx)
	sy := ty * ty * (3 - 2*ty)
	ix0 := v00 + (v10-v00)*sx
	ix1 := v01 + (v11-v01)*sx
	return ix0 + (ix1-ix0)*sy
}

func mathFloat32(f float64) uint32 { return math.Float32bits(float32(f)) }

func mathFloat64bits(f float64) uint64 { return math.Float64bits(f) }

func defaultColors() []string {
	return []string{
		"#FF5733", "#33FF57", "#3357FF", "#FF33A8", "#33FFF5",
		"#F5FF33", "#A833FF", "#FF8C33", "#33FF8C", "#8C33FF",
		"#FF3333", "#33FF33", "#3333FF", "#FF33FF", "#33FFFF",
		"#FFFF33", "#9933FF", "#FF9933", "#33FF99", "#3399FF",
	}
}
