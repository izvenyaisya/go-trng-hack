package main

import (
	"encoding/binary"
)

// TRNG implemented via HMAC-DRBG seeded with seed || perSeeds
type TRNG struct {
	drbg *HMACDRBG
}

func NewTRNGFromSeed(seed int64, per []int64) *TRNG {
	// build seed material: seed||perSeeds
	buf := make([]byte, 8*(1+len(per)))
	binary.LittleEndian.PutUint64(buf[0:8], uint64(seed))
	off := 8
	for i := 0; i < len(per); i++ {
		binary.LittleEndian.PutUint64(buf[off:off+8], uint64(per[i]))
		off += 8
	}
	drbg := newHMACDRBG(buf)
	return &TRNG{drbg: drbg}
}

func NewTRNGFromTx(tx *Transaction) *TRNG {
	return NewTRNGFromSeed(tx.Seed, tx.Provenance.PerHTTPSeeds)
}

func (t *TRNG) ReadBytes(n int) []byte {
	return t.drbg.Generate(n)
}
