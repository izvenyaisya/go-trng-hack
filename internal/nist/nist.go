package nist

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/NIST-Statistical-Test-Suite
#cgo LDFLAGS: ${SRCDIR}/../../third_party/NIST-Statistical-Test-Suite/libnist.a
#include <stdlib.h>

// Placeholder for NIST function declarations. Replace with actual headers
// from the NIST suite, for example:
// int monobit_test(const unsigned char *bits, unsigned long length, double *p_value);

*/
import "C"
import (
	"errors"
	"unsafe"
)

// RunMonobit runs the NIST monobit test on the provided bit slice.
// This is a thin wrapper — adjust the call to match the NIST signature.
func RunMonobit(bits []byte) (float64, error) {
	if len(bits) == 0 {
		return 0, errors.New("empty input")
	}

	// Convert to C pointer
	cptr := (*C.uchar)(unsafe.Pointer(&bits[0]))
	clen := C.ulong(len(bits))

	// avoid unused variable errors while the real C call is not wired yet
	_ = cptr
	_ = clen

	var pval C.double

	// Example: replace `monobit_test` with the real function name
	// ret := C.monobit_test(cptr, clen, &pval)
	ret := C.int(0) // placeholder

	if ret != 0 {
		return 0, errors.New("nist test returned error")
	}

	return float64(pval), nil
}
