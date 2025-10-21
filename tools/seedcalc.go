//go:build tools
// +build tools

package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
)

func main() {
	s := "4cd2488d4f59d1069e28040c2a265169dfab4a4e186a7053fbef9c327aa8396b"
	b, err := hex.DecodeString(s)
	if err != nil {
		log.Fatalf("decode hex: %v", err)
	}
	h := sha256.Sum256(b)
	hhex := hex.EncodeToString(h[:])
	u := binary.LittleEndian.Uint64(h[:8])
	seed := int64(u)
	fmt.Printf("decoded_len=%d\n", len(b))
	fmt.Printf("sha256=%s\n", hhex)
	fmt.Printf("seed_uint64=%d\n", u)
	fmt.Printf("seed_int64=%d\n", seed)
}
