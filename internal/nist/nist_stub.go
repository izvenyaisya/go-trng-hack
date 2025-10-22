//go:build !cgo
// +build !cgo

package nist

import "errors"

// RunMonobit stub when cgo is disabled. Returns a clear error to instruct the user.
func RunMonobit(bits []byte) (float64, error) {
	return 0, errors.New("nist CGO integration not built: enable cgo and build the NIST static library as described in NIST_INTEGRATION.md")
}
