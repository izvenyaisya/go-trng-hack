package main

import (
	"crypto/hmac"
	"crypto/sha256"
)

// Minimal HMAC-DRBG (SP800-90A style) using HMAC-SHA256.
// Not fully featured (no reseed interval), but suitable for deterministic generation from seed material.
type HMACDRBG struct {
	K []byte
	V []byte
}

func newHMACDRBG(seedMaterial []byte) *HMACDRBG {
	// initialize K = 0x00.., V = 0x01..
	K := make([]byte, 32)
	V := make([]byte, 32)
	for i := range V {
		V[i] = 0x01
	}
	d := &HMACDRBG{K: K, V: V}
	d.update(seedMaterial)
	return d
}

func (d *HMACDRBG) hmac(data []byte) []byte {
	mac := hmac.New(sha256.New, d.K)
	mac.Write(data)
	return mac.Sum(nil)
}

func (d *HMACDRBG) update(seedMaterial []byte) {
	// K = HMAC(K, V || 0x00 || seedMaterial)
	mac := hmac.New(sha256.New, d.K)
	mac.Write(d.V)
	mac.Write([]byte{0x00})
	if len(seedMaterial) > 0 {
		mac.Write(seedMaterial)
	}
	d.K = mac.Sum(nil)

	// V = HMAC(K, V)
	mac = hmac.New(sha256.New, d.K)
	mac.Write(d.V)
	d.V = mac.Sum(nil)

	if len(seedMaterial) > 0 {
		mac = hmac.New(sha256.New, d.K)
		mac.Write(d.V)
		mac.Write([]byte{0x01})
		mac.Write(seedMaterial)
		d.K = mac.Sum(nil)

		mac = hmac.New(sha256.New, d.K)
		mac.Write(d.V)
		d.V = mac.Sum(nil)
	}
}

// Generate n bytes
func (d *HMACDRBG) Generate(n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		mac := hmac.New(sha256.New, d.K)
		mac.Write(d.V)
		d.V = mac.Sum(nil)
		out = append(out, d.V...)
	}
	// per spec, update with no additional input
	d.update(nil)
	return out[:n]
}
