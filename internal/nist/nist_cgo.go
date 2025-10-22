//go:build cgo
// +build cgo

package nist

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/NIST-Statistical-Test-Suite
#cgo LDFLAGS: ${SRCDIR}/../../third_party/NIST-Statistical-Test-Suite/libnist.a
#include <stdlib.h>

// Include appropriate headers from the NIST STS here when available.
*/
import "C"

import "errors"

// RunMonobit is a placeholder cgo implementation. Replace with a real call
// to the NIST STS function once the library and headers are added.
func RunMonobit(bits []byte) (float64, error) {
	return 0, errors.New("nist cgo wrapper not yet implemented: build libnist.a and update internal/nist/nist_cgo.go to call the real function")
}
