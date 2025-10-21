//go:build tools
// +build tools

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	base := "http://localhost:4040"
	// 1) generate
	resp, err := http.Get(base + "/generate")
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Println("--- /generate response ---")
	fmt.Println(string(b))
	var gen map[string]interface{}
	if err := json.Unmarshal(b, &gen); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse generate JSON: %v\n", err)
		os.Exit(1)
	}
	id, _ := gen["tx_id"].(string)
	if id == "" {
		fmt.Fprintln(os.Stderr, "no tx_id in generate response")
		os.Exit(1)
	}

	// 2) stats
	resp2, err := http.Get(base + "/tx/" + id + "/stats?mode=nist")
	if err != nil {
		fmt.Fprintf(os.Stderr, "stats request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp2.Body.Close()
	b2, _ := io.ReadAll(resp2.Body)
	fmt.Println("--- /tx/{id}/stats?mode=nist response ---")
	fmt.Println(string(b2))
}
