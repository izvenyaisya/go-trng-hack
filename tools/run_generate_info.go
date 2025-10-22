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
	// generate
	resp, err := http.Get(base + "/generate")
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
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
	fmt.Println("generated tx:", id)

	// fetch info
	resp2, err := http.Get(base + "/tx/" + id + "/info")
	if err != nil {
		fmt.Fprintf(os.Stderr, "info request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp2.Body.Close()
	b2, _ := io.ReadAll(resp2.Body)
	// pretty print per_http_seeds
	var info map[string]interface{}
	if err := json.Unmarshal(b2, &info); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse info JSON: %v\n", err)
		os.Exit(1)
	}
	// info["tx"] -> provenance -> PerHTTPSeeds
	tx, _ := info["tx"].(map[string]interface{})
	if tx == nil {
		fmt.Println(string(b2))
		return
	}
	prov, _ := tx["Provenance"].(map[string]interface{})
	if prov == nil {
		// try lowercase
		prov, _ = tx["provenance"].(map[string]interface{})
	}
	if prov == nil {
		fmt.Println(string(b2))
		return
	}
	per, _ := prov["PerHTTPSeeds"]
	if per == nil {
		per = prov["per_http_seeds"]
	}
	fmt.Printf("PerHTTPSeeds: %v\n", per)
}
