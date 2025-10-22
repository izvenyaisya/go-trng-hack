package main

import (
	"fmt"
	"log"
	"os"

	"rng-chaos/internal/nist"
)

func main() {
	// For demonstration, read a file of bytes (raw bits or bytes) from arg1
	if len(os.Args) < 2 {
		log.Fatalf("usage: run_nist <input-file>")
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatalf("read input: %v", err)
	}

	p, err := nist.RunMonobit(b)
	if err != nil {
		log.Fatalf("nist test: %v", err)
	}

	fmt.Printf("Monobit p-value: %f\n", p)
}
