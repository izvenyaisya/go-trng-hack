// stats.go
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"
)

/* ===========================
   МАТЕМАТИКА (p-values)
   =========================== */

func erfc(x float64) float64 { return math.Erfc(x) }

func normalCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

// igamc: complemented incomplete gamma Q(a,x)
func igamc(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return math.NaN()
	}
	const EPS = 1e-14
	const FPMIN = 1e-300
	gln, _ := math.Lgamma(a)
	// серия для нижней неполной гаммы => Q = 1 - P
	if x < a+1 {
		ap := a
		sum := 1.0 / a
		del := sum
		for n := 1; n < 1000; n++ {
			ap += 1
			del *= x / ap
			sum += del
			if math.Abs(del) < math.Abs(sum)*EPS {
				break
			}
		}
		ret := sum * math.Exp(-x+a*math.Log(x)-gln)
		return 1.0 - ret
	}
	// непрерывная дробь для верхней
	b := x + 1 - a
	c := 1.0 / FPMIN
	d := 1.0 / b
	h := d
	for i := 1; i < 1000; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2.0
		d = an*d + b
		if math.Abs(d) < FPMIN {
			d = FPMIN
		}
		c = b + an/c
		if math.Abs(c) < FPMIN {
			c = FPMIN
		}
		d = 1.0 / d
		del := d * c
		h *= del
		if math.Abs(del-1.0) < EPS {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-gln) * h
}

/* ===========================
   УТИЛИТЫ
   =========================== */

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minFloat(vals ...float64) float64 {
	if len(vals) == 0 {
		return math.NaN()
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
func sumInt(xs []int) int {
	s := 0
	for _, v := range xs {
		s += v
	}
	return s
}

/* ===========================
   ПАРСИНГ БИТ
   =========================== */

type FileMode int

const (
	FileModeTXT          FileMode = iota // текст '0'/'1' (остальное игнор)
	FileModeBinBytes01                   // бинарь: каждый байт 0x00/0x01 = один бит
	FileModeBinPackedMSB                 // бинарь: упакованные биты, MSB-first
)

func AnalyzeBitsFromString(bitsStr string) ([]int, error) {
	out := make([]int, 0, len(bitsStr))
	for _, r := range bitsStr {
		switch r {
		case '0':
			out = append(out, 0)
		case '1':
			out = append(out, 1)
		default:
			if unicode.IsSpace(r) {
				continue
			}
			// игнорируем прочие
		}
	}
	if len(out) == 0 {
		return nil, errors.New("в строке не найдено битов 0/1")
	}
	return out, nil
}

func parseBinBytes01(b []byte) ([]int, error) {
	out := make([]int, 0, len(b))
	for i, by := range b {
		switch by {
		case 0x00:
			out = append(out, 0)
		case 0x01:
			out = append(out, 1)
		default:
			return nil, fmt.Errorf("байт #%d=0x%02X не 0x00/0x01", i, by)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("пустой бинарный файл")
	}
	return out, nil
}

func unpackBitsMSB(b []byte) []int {
	out := make([]int, 0, len(b)*8)
	for _, by := range b {
		for bit := 7; bit >= 0; bit-- {
			if ((by >> uint(bit)) & 1) == 1 {
				out = append(out, 1)
			} else {
				out = append(out, 0)
			}
		}
	}
	return out
}

// эвристика: если все байты ∈{0x00,0x01} и их много — считаем bin01, иначе packed
func guessBinMode(b []byte) FileMode {
	if len(b) == 0 {
		return FileModeBinPackedMSB
	}
	all01 := true
	for _, by := range b {
		if by != 0 && by != 1 {
			all01 = false
			break
		}
	}
	if all01 {
		return FileModeBinBytes01
	}
	return FileModeBinPackedMSB
}

/* ===========================
   ТЕСТЫ (14)
   =========================== */

// 1) Frequency (Monobit)
func testFrequency(seq []int) map[string]any {
	n := len(seq)
	if n < 100 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	sum := 0
	for _, b := range seq {
		if b == 1 {
			sum++
		} else {
			sum--
		}
	}
	sObs := math.Abs(float64(sum)) / math.Sqrt(float64(n))
	p := erfc(sObs / math.Sqrt2)
	return map[string]any{"pValue": p, "n": n, "sSum": sum, "sObs": sObs}
}

// 2) Block Frequency (M=128)
func testBlockFrequency(seq []int, M int) map[string]any {
	n := len(seq)
	if n < 100 || M <= 0 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	N := n / M
	if N == 0 {
		return map[string]any{"isError": true, "n": n, "M": M, "N": 0, "pValue": math.NaN()}
	}
	chi := 0.0
	for i := 0; i < N; i++ {
		sum := 0
		for j := i * M; j < i*M+M; j++ {
			sum += seq[j]
		}
		pi := float64(sum) / float64(M)
		chi += math.Pow(pi-0.5, 2)
	}
	chi *= 4.0 * float64(M)
	p := igamc(float64(N)/2.0, chi/2.0)
	return map[string]any{"pValue": p, "n": n, "M": M, "N": N, "chiSqr": chi}
}

// 3) Runs
func testRuns(seq []int) map[string]any {
	n := len(seq)
	if n < 100 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	sumOnes := sumInt(seq)
	pi := float64(sumOnes) / float64(n)
	tau := 2.0 / math.Sqrt(float64(n))
	if math.Abs(pi-0.5) > tau {
		return map[string]any{"isError": true, "n": n, "piObs": pi, "tau": tau, "pValue": math.NaN()}
	}
	vObs := 1
	for i := 1; i < n; i++ {
		if seq[i] != seq[i-1] {
			vObs++
		}
	}
	temp := (float64(vObs) - 2.0*float64(n)*pi*(1.0-pi)) / (2.0 * pi * (1.0 - pi) * math.Sqrt(2.0*float64(n)))
	p := erfc(math.Abs(temp))
	return map[string]any{"pValue": p, "n": n, "vObs": vObs, "piObs": pi}
}

// 4) Longest Run of Ones in a Block
func testLongestRun(seq []int) map[string]any {
	n := len(seq)
	if n < 128 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	var K, M int
	var piVal []float64
	switch {
	case n < 6272:
		K, M = 3, 8
		piVal = []float64{0.21484375, 0.3671875, 0.23046875, 0.1875}
	case n < 750000:
		K, M = 5, 128
		piVal = []float64{0.1174035788, 0.242955959, 0.249363483, 0.17517706, 0.102701071, 0.112398847}
	default:
		K, M = 6, 10000
		piVal = []float64{0.0882, 0.2092, 0.2483, 0.1933, 0.1208, 0.0675, 0.0727}
	}
	N := n / M
	nu := make([]int, K+1)
	for i := 0; i < N; i++ {
		maxRun, cur := 0, 0
		for j := 0; j < M; j++ {
			if seq[i*M+j] == 1 {
				cur++
				if cur > maxRun {
					maxRun = cur
				}
			} else {
				cur = 0
			}
		}
		if maxRun < len(nu) {
			nu[maxRun]++
		} else {
			nu[K]++
		}
	}
	chi := 0.0
	for i := 0; i <= K; i++ {
		chi += math.Pow(float64(nu[i])-float64(N)*piVal[i], 2) / (float64(N) * piVal[i])
	}
	p := igamc(float64(K)/2.0, chi/2.0)
	return map[string]any{"pValue": p, "n": n, "M": M, "N": N, "chiSqr": chi}
}

// 5) Binary Matrix Rank (32x32)
func testBinaryMatrixRank(seq []int) map[string]any {
	n := len(seq)
	rows, cols := 32, 32
	if n < 38*rows*cols { // 38912 бит
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	N := n / (rows * cols)
	F32, F31 := 0, 0
	mat := make([]uint32, rows)
	for k := 0; k < N; k++ {
		offset := k * rows * cols
		for i := 0; i < rows; i++ {
			var row uint32
			for j := 0; j < cols; j++ {
				row <<= 1
				if seq[offset+i*cols+j] == 1 {
					row |= 1
				}
			}
			mat[i] = row
		}
		R := rankGF2_32(mat)
		if R == 32 {
			F32++
		} else if R == 31 {
			F31++
		}
	}
	F30 := N - (F32 + F31)
	p32 := probRankGeneric(32, 32, 32)
	p31 := probRankGeneric(31, 32, 32)
	p30 := 1 - (p32 + p31)
	chi := math.Pow(float64(F32)-float64(N)*p32, 2)/(float64(N)*p32) +
		math.Pow(float64(F31)-float64(N)*p31, 2)/(float64(N)*p31) +
		math.Pow(float64(F30)-float64(N)*p30, 2)/(float64(N)*p30)
	p := math.Exp(-chi / 2.0) // df=2
	return map[string]any{"pValue": p, "n": n, "N": N, "F32": F32, "F31": F31, "F30": F30, "chiSqr": chi}
}

func rankGF2_32(a []uint32) int {
	mat := make([]uint32, len(a))
	copy(mat, a)
	rank := 0
	for col := 31; col >= 0; col-- {
		pivot := -1
		mask := uint32(1) << uint(col)
		for r := rank; r < 32; r++ {
			if (mat[r] & mask) != 0 {
				pivot = r
				break
			}
		}
		if pivot == -1 {
			continue
		}
		mat[rank], mat[pivot] = mat[pivot], mat[rank]
		for r := 0; r < 32; r++ {
			if r != rank && (mat[r]&mask) != 0 {
				mat[r] ^= mat[rank]
			}
		}
		rank++
		if rank == 32 {
			break
		}
	}
	return rank
}

func probRankGeneric(r, m, n int) float64 {
	R, M, N := float64(r), float64(m), float64(n)
	prod := 1.0
	for i := 0.0; i <= R-1; i++ {
		num := (1 - math.Pow(2, i-M)) * (1 - math.Pow(2, i-N))
		den := (1 - math.Pow(2, i-R))
		prod *= num / den
	}
	return math.Pow(2, R*(M+N-R)-M*N) * prod
}

// 6) Non-Overlapping Template (один шаблон '111...1', m=9)
func testNonOverlappingTemplate(seq []int, m int) map[string]any {
	n := len(seq)
	if n < 1000000 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	N := 8
	M := n / N
	if M <= m {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	tpl := make([]int, m)
	for i := 0; i < m; i++ {
		tpl[i] = 1
	}
	lambda := float64(M-m+1) / math.Pow(2, float64(m))
	varWj := float64(M) * (1.0/math.Pow(2, float64(m)) - (2.0*float64(m)-1.0)/math.Pow(2, float64(2*m)))
	Wj := make([]int, N)
	for i := 0; i < N; i++ {
		W := 0
		for j := 0; j <= M-m; {
			match := true
			for k := 0; k < m; k++ {
				if seq[i*M+j+k] != tpl[k] {
					match = false
					break
				}
			}
			if match {
				W++
				j += m
			} else {
				j++
			}
		}
		Wj[i] = W
	}
	chi := 0.0
	for i := 0; i < N; i++ {
		chi += math.Pow((float64(Wj[i])-lambda)/math.Sqrt(varWj), 2)
	}
	p := igamc(float64(N)/2.0, chi/2.0)
	return map[string]any{"pValue": p, "n": n, "m": m, "M": M, "N": N, "chiSqr": chi}
}

// 7) Overlapping Template (m=9, шаблон '111...1')
func testOverlappingTemplate(seq []int, m int) map[string]any {
	n := len(seq)
	if n < 1000000 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	M := 1032
	N := n / M
	if N == 0 || M <= m {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	tpl := make([]int, m)
	for i := 0; i < m; i++ {
		tpl[i] = 1
	}
	lambda := float64(M-m+1) / math.Pow(2, float64(m))
	eta := lambda / 2.0
	K := 5
	pi := make([]float64, K+1)
	sum := 0.0
	for i := 0; i < K; i++ {
		pi[i] = prOverlapping(i, eta)
		sum += pi[i]
	}
	pi[K] = 1 - sum

	nu := make([]int, K+1)
	for i := 0; i < N; i++ {
		W := 0
		for j := 0; j <= M-m; j++ {
			match := true
			for k := 0; k < m; k++ {
				if seq[i*M+j+k] != tpl[k] {
					match = false
					break
				}
			}
			if match {
				W++
			}
		}
		if W <= 4 {
			nu[W]++
		} else {
			nu[K]++
		}
	}
	chi := 0.0
	for i := 0; i <= K; i++ {
		exp := float64(N) * pi[i]
		chi += math.Pow(float64(nu[i])-exp, 2) / exp
	}
	p := igamc(float64(K)/2.0, chi/2.0)
	return map[string]any{"pValue": p, "n": n, "m": m, "M": M, "N": N, "chiSqr": chi}
}

func prOverlapping(u int, eta float64) float64 {
	if u == 0 {
		return math.Exp(-eta)
	}
	sum := 0.0
	for l := 1; l <= u; l++ {
		sum += math.Exp(-eta - float64(u)*math.Ln2 + float64(l)*math.Log(eta) + lgamma(float64(u)) - lgamma(float64(l)) - lgamma(float64(u-l+1)))
	}
	return sum
}
func lgamma(x float64) float64 { y, _ := math.Lgamma(x); return y }

// 8) Maurer’s Universal
func testUniversalMaurer(seq []int) map[string]any {
	n := len(seq)
	if n < 387840 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	L := 5
	switch {
	case n >= 1059061760:
		L = 16
	case n >= 496435200:
		L = 15
	case n >= 231669760:
		L = 14
	case n >= 107560960:
		L = 13
	case n >= 49643520:
		L = 12
	case n >= 22753280:
		L = 11
	case n >= 10342400:
		L = 10
	case n >= 4654080:
		L = 9
	case n >= 2068480:
		L = 8
	case n >= 904960:
		L = 7
	case n >= 387840:
		L = 6
	}
	Q := 10 * (1 << L)
	K := n/L - Q
	if K <= 0 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	p := 1 << L
	expected := []float64{0, 0, 0, 0, 0, 0, 5.2177052, 6.1962507, 7.1836656, 8.1764248, 9.1723243, 10.170032, 11.168765, 12.168070, 13.167693, 14.167488, 15.167379}
	variance := []float64{0, 0, 0, 0, 0, 0, 2.954, 3.125, 3.238, 3.311, 3.356, 3.384, 3.401, 3.410, 3.416, 3.419, 3.421}
	T := make([]int, p)

	for i := 0; i < Q; i++ {
		idx := 0
		for j := 0; j < L; j++ {
			idx = (idx << 1) + seq[i*L+j]
		}
		T[idx] = i + 1
	}
	sum := 0.0
	for i := Q; i < Q+K; i++ {
		idx := 0
		for j := 0; j < L; j++ {
			idx = (idx << 1) + seq[i*L+j]
		}
		sum += math.Log(float64(i+1-T[idx])) / math.Log(2)
		T[idx] = i + 1
	}
	phi := sum / float64(K)
	c := 0.7 - 0.8/float64(L) + (4.0+32.0/float64(L))*math.Pow(float64(K), -3.0/float64(L))/15.0
	sigma := c * math.Sqrt(variance[L]/float64(K))
	arg := math.Abs(phi-expected[L]) / (math.Sqrt2 * sigma)
	pv := erfc(arg)
	return map[string]any{"pValue": pv, "n": n, "L": L, "Q": Q, "K": K, "phi": phi, "expected": expected[L], "sigma": sigma}
}

// 9) Linear Complexity (M=1000)
func testLinearComplexity(seq []int, M int) map[string]any {
	n := len(seq)
	if n < 1000000 || M <= 0 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	K := n / M
	if K == 0 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	pi := []float64{0.01047, 0.03125, 0.12500, 0.50000, 0.25000, 0.06250, 0.020833}
	nu := make([]float64, 7)

	for blk := 0; blk < K; blk++ {
		L := 0
		m := -1
		C := make([]int, M)
		B := make([]int, M)
		C[0], B[0] = 1, 1
		for N_ := 0; N_ < M; N_++ {
			d := seq[blk*M+N_]
			for i := 1; i <= L; i++ {
				d ^= C[i] & seq[blk*M+N_-i]
			}
			if d == 1 {
				T := make([]int, M)
				copy(T, C)
				for j := 0; j < M; j++ {
					if B[j] == 1 && j+N_-m < M {
						C[j+N_-m] ^= 1
					}
				}
				if L <= N_/2 {
					L = N_ + 1 - L
					m = N_
					B = T
				}
			}
		}
		sign := 1.0
		if (M+1)%2 == 0 {
			sign = -1.0
		}
		mean := float64(M)/2.0 + (9.0+sign)/36.0 - (1.0/math.Pow(2, float64(M)))*(float64(M)/3.0+2.0/9.0)
		if M%2 != 0 {
			sign = -1.0
		} else {
			sign = 1.0
		}
		Tp := sign*(float64(L)-mean) + 2.0/9.0

		switch {
		case Tp <= -2.5:
			nu[0]++
		case Tp <= -1.5:
			nu[1]++
		case Tp <= -0.5:
			nu[2]++
		case Tp <= 0.5:
			nu[3]++
		case Tp <= 1.5:
			nu[4]++
		case Tp <= 2.5:
			nu[5]++
		default:
			nu[6]++
		}
	}
	chi := 0.0
	for i := 0; i < 7; i++ {
		exp := float64(K) * pi[i]
		chi += math.Pow(nu[i]-exp, 2) / exp
	}
	p := igamc(6.0/2.0, chi/2.0)
	return map[string]any{"pValue": p, "n": n, "M": M, "K": K, "chiSqr": chi}
}

// 10) Serial (m=2)
func testSerial(seq []int, m int) map[string]any {
	n := len(seq)
	if n < 1000000 || m < 2 {
		return map[string]any{"isError": true, "n": n, "pValue1": math.NaN(), "pValue2": math.NaN()}
	}
	psi := func(mm int) float64 {
		if mm <= 0 {
			return 0
		}
		P := make([]int, 1<<(mm+1))
		for i := 0; i < n; i++ {
			k := 1
			for j := 0; j < mm; j++ {
				if seq[(i+j)%n] == 0 {
					k *= 2
				} else {
					k = 2*k + 1
				}
			}
			P[k-1]++
		}
		sum := 0.0
		for i := (1 << mm) - 1; i <= (1<<(mm+1))-2; i++ {
			sum += float64(P[i] * P[i])
		}
		return (sum*float64(int(1<<mm)))/float64(n) - float64(n)
	}
	psim0 := psi(m)
	psim1 := psi(m - 1)
	psim2 := psi(m - 2)
	del1 := psim0 - psim1
	del2 := psim0 - 2.0*psim1 + psim2
	p1 := igamc(float64(int(1<<(m-1)))/2.0, del1/2.0)
	p2 := igamc(float64(int(1<<(m-2)))/2.0, del2/2.0)
	return map[string]any{"pValue1": p1, "pValue2": p2, "n": n, "m": m}
}

// 11) Approximate Entropy (m=2)
func testApproxEntropy(seq []int, m int) map[string]any {
	n := len(seq)
	if n < 100 {
		return map[string]any{"isError": true, "n": n, "pValue": math.NaN()}
	}
	seqWrap := make([]int, n+m)
	copy(seqWrap, seq)
	copy(seqWrap[n:], seq[:m])

	Ap := make([]float64, 2)
	for bl := m; bl <= m+1; bl++ {
		P := make([]int, 1<<bl)
		for i := 0; i < n; i++ {
			idx := 0
			for j := 0; j < bl; j++ {
				idx = (idx << 1) | seqWrap[i+j]
			}
			P[idx]++
		}
		sum := 0.0
		for i := 0; i < len(P); i++ {
			if P[i] > 0 {
				sum += float64(P[i]) * math.Log(float64(P[i])/float64(n))
			}
		}
		Ap[bl-m] = sum / float64(n)
	}
	apen := Ap[0] - Ap[1]
	chi := 2.0 * float64(n) * (math.Log(2.0) - apen)
	p := igamc(float64(int(1<<(m-1)))/2.0, chi/2.0)
	return map[string]any{"pValue": p, "n": n, "m": m, "apen": apen, "chiSqr": chi}
}

// 12) Cumulative Sums (FWD/REV)
func testCumulativeSums(seq []int) map[string]any {
	n := len(seq)
	if n < 100 {
		return map[string]any{"isError": true, "n": n, "pValueFWD": math.NaN(), "pValueREV": math.NaN()}
	}
	// forward
	S, sup, inf := 0, 0, 0
	for k := 0; k < n; k++ {
		if seq[k] == 1 {
			S++
		} else {
			S--
		}
		if S > sup {
			sup++
		}
		if S < inf {
			inf--
		}
	}
	z := maxInt(sup, -inf)
	if z == 0 {
		return map[string]any{"isError": true, "n": n, "pValueFWD": math.NaN(), "pValueREV": math.NaN()}
	}
	sum1 := 0.0
	for k := int(math.Trunc((float64(-n)/float64(z) + 1.0) / 4.0)); k <= int(math.Trunc((float64(n)/float64(z)-1.0)/4.0)); k++ {
		sum1 += normalCDF(((4.0*float64(k)+1.0)*float64(z))/math.Sqrt(float64(n))) - normalCDF(((4.0*float64(k)-1.0)*float64(z))/math.Sqrt(float64(n)))
	}
	sum2 := 0.0
	for k := int(math.Trunc((float64(-n)/float64(z) - 3.0) / 4.0)); k <= int(math.Trunc((float64(n)/float64(z)-1.0)/4.0)); k++ {
		sum2 += normalCDF(((4.0*float64(k)+3.0)*float64(z))/math.Sqrt(float64(n))) - normalCDF(((4.0*float64(k)+1.0)*float64(z))/math.Sqrt(float64(n)))
	}
	pF := 1.0 - sum1 + sum2

	// reverse
	S, sup, inf = 0, 0, 0
	for k := n - 1; k >= 0; k-- {
		if seq[k] == 1 {
			S++
		} else {
			S--
		}
		if S > sup {
			sup++
		}
		if S < inf {
			inf--
		}
	}
	zr := maxInt(sup, -inf)
	if zr == 0 {
		return map[string]any{"pValueFWD": pF, "pValueREV": 1.0}
	}
	sum1 = 0
	for k := int(math.Trunc((float64(-n)/float64(zr) + 1.0) / 4.0)); k <= int(math.Trunc((float64(n)/float64(zr)-1.0)/4.0)); k++ {
		sum1 += normalCDF(((4.0*float64(k)+1.0)*float64(zr))/math.Sqrt(float64(n))) - normalCDF(((4.0*float64(k)-1.0)*float64(zr))/math.Sqrt(float64(n)))
	}
	sum2 = 0
	for k := int(math.Trunc((float64(-n)/float64(zr) - 3.0) / 4.0)); k <= int(math.Trunc((float64(n)/float64(zr)-1.0)/4.0)); k++ {
		sum2 += normalCDF(((4.0*float64(k)+3.0)*float64(zr))/math.Sqrt(float64(n))) - normalCDF(((4.0*float64(k)+1.0)*float64(zr))/math.Sqrt(float64(n)))
	}
	pR := 1.0 - sum1 + sum2
	return map[string]any{"pValueFWD": pF, "pValueREV": pR}
}

// 13) Random Excursions
func testRandomExcursions(seq []int) map[string]any {
	n := len(seq)
	if n < 1000000 {
		return map[string]any{"isError": true, "n": n, "minPValue": math.NaN()}
	}
	S := make([]int, n)
	S[0] = 2*seq[0] - 1
	J := 0
	cycles := make([]int, 0, n/10)
	for i := 1; i < n; i++ {
		S[i] = S[i-1] + 2*seq[i] - 1
		if S[i] == 0 {
			J++
			cycles = append(cycles, i)
		}
	}
	if S[n-1] != 0 {
		J++
	}
	cycles = append(cycles, n)
	constraint := math.Max(0.005*math.Sqrt(float64(n)), 500)
	if float64(J) < constraint {
		return map[string]any{"isError": true, "n": n, "cycleCount": J, "minPValue": math.NaN()}
	}
	stateX := []int{-4, -3, -2, -1, 1, 2, 3, 4}
	pi := [][]float64{
		{0, 0, 0, 0, 0, 0},
		{0.5, 0.25, 0.125, 0.0625, 0.03125, 0.03125},
		{0.75, 0.0625, 0.046875, 0.03515625, 0.0263671875, 0.0791015625},
		{0.8333333333, 0.02777777778, 0.02314814815, 0.01929012346, 0.01607510288, 0.0803755143},
		{0.875, 0.015625, 0.013671875, 0.01196289063, 0.0104675293, 0.0732727051},
	}
	nu := make([][]float64, 6)
	for i := range nu {
		nu[i] = make([]float64, 8)
	}
	start := 0
	stop := cycles[0]
	for j := 1; j <= J; j++ {
		counter := make([]int, 8)
		for i := start; i < stop; i++ {
			if (S[i] >= 1 && S[i] <= 4) || (S[i] >= -4 && S[i] <= -1) {
				b := 3
				if S[i] < 0 {
					b = 4
				}
				counter[S[i]+b]++
			}
		}
		start = cycles[j-1] + 1
		if j < J {
			stop = cycles[j]
		}
		for i := 0; i < 8; i++ {
			if counter[i] >= 0 && counter[i] <= 4 {
				nu[counter[i]][i]++
			} else if counter[i] >= 5 {
				nu[5][i]++
			}
		}
	}
	pvals := make([]float64, 8)
	for i := 0; i < 8; i++ {
		x := stateX[i]
		sum := 0.0
		for k := 0; k < 6; k++ {
			exp := float64(J) * pi[int(math.Abs(float64(x)))][k]
			sum += math.Pow(nu[k][i]-exp, 2) / exp
		}
		pvals[i] = igamc(2.5, sum/2.0)
	}
	minp := pvals[0]
	for i := 1; i < len(pvals); i++ {
		if pvals[i] < minp {
			minp = pvals[i]
		}
	}
	return map[string]any{"pValue": pvals, "minPValue": minp, "n": n, "cycleCount": J}
}

// 14) Random Excursions Variant
func testRandomExcursionsVariant(seq []int) map[string]any {
	n := len(seq)
	if n < 1000000 {
		return map[string]any{"isError": true, "n": n, "minPValue": math.NaN()}
	}
	S := make([]int, n)
	S[0] = 2*seq[0] - 1
	J := 0
	for i := 1; i < n; i++ {
		S[i] = S[i-1] + 2*seq[i] - 1
		if S[i] == 0 {
			J++
		}
	}
	if S[n-1] != 0 {
		J++
	}
	constraint := math.Max(0.005*math.Sqrt(float64(n)), 500)
	if float64(J) < constraint {
		return map[string]any{"isError": true, "n": n, "cycleCount": J, "minPValue": math.NaN()}
	}
	stateX := []int{-9, -8, -7, -6, -5, -4, -3, -2, -1, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	pvals := make([]float64, len(stateX))
	for p := 0; p < len(stateX); p++ {
		x := stateX[p]
		count := 0
		for i := 0; i < n; i++ {
			if S[i] == x {
				count++
			}
		}
		pvals[p] = erfc(math.Abs(float64(count)-float64(J)) / math.Sqrt(2.0*float64(J)*(4.0*math.Abs(float64(x))-2.0)))
	}
	minp := pvals[0]
	for i := 1; i < len(pvals); i++ {
		if pvals[i] < minp {
			minp = pvals[i]
		}
	}
	return map[string]any{"pValue": pvals, "minPValue": minp, "n": n, "cycleCount": J}
}

/* ===========================
   СВОДКА/ТАБЛИЦА
   =========================== */

type TestRow struct {
	Name   string             `json:"name"`
	Values map[string]float64 `json:"values"`
	Status string             `json:"status"`
}

func statusFromP(p float64) string {
	if p >= 0.01 && !math.IsNaN(p) {
		return "Passed"
	}
	return "Failed"
}
func statusFromAll(ps ...float64) string {
	for _, p := range ps {
		if !(p >= 0.01) {
			return "Failed"
		}
	}
	return "Passed"
}

func buildReportTable(tests map[string]any) []TestRow {
	// Keep only core tests: Frequency, Block Frequency, Runs, Serial (m=2),
	// Approximate Entropy (m=2), and Cumulative Sums.
	get := func(key, field string) float64 {
		if r, ok := tests[key].(map[string]any); ok {
			if v, ok := r[field]; ok {
				if f, ok := v.(float64); ok {
					return f
				}
			}
		}
		return math.NaN()
	}
	rows := make([]TestRow, 0, 6)
	rows = append(rows, TestRow{"1. Frequency (Monobit) Test", map[string]float64{"pValue": get("frequency", "pValue")}, statusFromP(get("frequency", "pValue"))})
	rows = append(rows, TestRow{"2. Frequency Test within a Block", map[string]float64{"pValue": get("frequency_block", "pValue")}, statusFromP(get("frequency_block", "pValue"))})
	rows = append(rows, TestRow{"3. Runs Test", map[string]float64{"pValue": get("runs", "pValue")}, statusFromP(get("runs", "pValue"))})
	rows = append(rows, TestRow{"4. Serial Test (m=2)", map[string]float64{"pValue1": get("serial_m2", "pValue1"), "pValue2": get("serial_m2", "pValue2")}, statusFromAll(get("serial_m2", "pValue1"), get("serial_m2", "pValue2"))})
	rows = append(rows, TestRow{"5. Approximate Entropy Test (m=2)", map[string]float64{"pValue": get("approx_entropy_m2", "pValue")}, statusFromP(get("approx_entropy_m2", "pValue"))})
	rows = append(rows, TestRow{"6. Cumulative Sums (Cusum) Test", map[string]float64{"pValueFWD": get("cumulative_sums", "pValueFWD"), "pValueREV": get("cumulative_sums", "pValueREV")}, statusFromAll(get("cumulative_sums", "pValueFWD"), get("cumulative_sums", "pValueREV"))})
	return rows
}

/* ===========================
   СВОДКА ТЕСТОВ
   =========================== */

func ComputeAllTests(seq []int) (map[string]any, []TestRow) {
	// Only compute the core tests to reduce runtime and output size.
	tests := make(map[string]any)
	tests["frequency"] = testFrequency(seq)
	tests["frequency_block"] = testBlockFrequency(seq, 128)
	tests["runs"] = testRuns(seq)
	tests["serial_m2"] = testSerial(seq, 2)
	tests["approx_entropy_m2"] = testApproxEntropy(seq, 2)
	tests["cumulative_sums"] = testCumulativeSums(seq)
	return tests, buildReportTable(tests)
}

/* ===========================
   HTTP ХЕНДЛЕРЫ (совместимо с api.go)
   =========================== */

// GET /tx/{id}/stats  — как раньше: берём TRNG из tx и считаем
func txStats(w http.ResponseWriter, r *http.Request, id string) {
	tx := mustTx(id, w)
	if tx == nil {
		return
	}
	// читаем байты из TRNG и распаковываем биты MSB-first, как раньше
	nBits := tx.Count
	tr := NewTRNGFromTx(tx)
	needed := (nBits + 7) / 8
	data := tr.ReadBytes(needed)
	// обнуляем лишние младшие биты
	if nBits%8 != 0 && needed > 0 {
		keep := uint(nBits % 8)
		mask := byte(0xFF) << (8 - keep)
		data[needed-1] &= mask
	}
	// распаковать
	n := int(nBits)
	seq := make([]int, 0, n)
	idx := 0
	for i := 0; i < len(data) && idx < n; i++ {
		b := data[i]
		for bit := 7; bit >= 0 && idx < n; bit-- {
			if ((b >> uint(bit)) & 1) == 1 {
				seq = append(seq, 1)
			} else {
				seq = append(seq, 0)
			}
			idx++
		}
	}
	tests, report := ComputeAllTests(seq)
	resp := map[string]any{
		"tx_id": tx.TxID,
		"count": tx.Count,
	}

	// sanitize tests/report to avoid json encoder errors on NaN/Inf
	resp["tests"] = sanitizeForJSON(tests)
	resp["report"] = sanitizeReport(report)

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(resp)
}

// POST /stats  — в body строка 0/1 ИЛИ multipart с файлом (txt/bin)
func uploadStatsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Пытаемся распарсить как multipart
	ct := r.Header.Get("Content-Type")
	var bits []int
	var err error

	modeParam := r.URL.Query().Get("mode") // txt | bin01 | binpacked
	if modeParam == "" {
		_ = r.ParseMultipartForm(32 << 20)
		modeParam = r.FormValue("mode")
	}

	if strings.HasPrefix(ct, "multipart/form-data") || r.MultipartForm != nil {
		bits, err = bitsFromMultipart(r, modeParam)
	} else {
		// raw body: пробуем как строка 0/1, если не похоже — бинарная эвристика
		body, e := io.ReadAll(r.Body)
		if e != nil {
			err = e
		} else {
			trim := strings.TrimSpace(string(body))
			if looksLikeBitsString(trim) {
				bits, err = AnalyzeBitsFromString(trim)
			} else {
				// бинарь
				bmode := modeFromString(modeParam)
				if bmode == -1 {
					bmode = guessBinMode(body)
				}
				switch bmode {
				case FileModeTXT:
					bits, err = AnalyzeBitsFromString(string(body))
				case FileModeBinBytes01:
					bits, err = parseBinBytes01(body)
				case FileModeBinPackedMSB:
					bits = unpackBitsMSB(body)
					err = nil
				}
			}
		}
	}

	if err != nil {
		http.Error(w, "failed to parse bits: "+err.Error(), http.StatusBadRequest)
		return
	}

	tests, report := ComputeAllTests(bits)
	resp := map[string]any{
		"n": len(bits),
	}
	resp["tests"] = sanitizeForJSON(tests)
	resp["report"] = sanitizeReport(report)

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(resp)
}

// sanitizeForJSON recursively walks common composite types and replaces
// float64 NaN/Inf with nil so encoding/json doesn't fail with
// "json: unsupported value: NaN". It handles maps, slices/arrays and
// several numeric types; for unknown types it falls back to the original
// value which is usually safe for the structures produced by ComputeAllTests.
func sanitizeForJSON(v any) any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil
		}
		return t
	case float32:
		fv := float64(t)
		if math.IsNaN(fv) || math.IsInf(fv, 0) {
			return nil
		}
		return fv
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		bool, string:
		return t
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = sanitizeForJSON(val)
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = sanitizeForJSON(el)
		}
		return out
	case []float64:
		out := make([]any, len(t))
		for i, el := range t {
			if math.IsNaN(el) || math.IsInf(el, 0) {
				out[i] = nil
			} else {
				out[i] = el
			}
		}
		return out
	}

	// Generic handling for slices/arrays/maps produced with concrete types
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		l := rv.Len()
		out := make([]any, l)
		for i := 0; i < l; i++ {
			out[i] = sanitizeForJSON(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		out := make(map[string]any)
		for _, key := range rv.MapKeys() {
			// try to stringify map key
			kstr := fmt.Sprint(key.Interface())
			out[kstr] = sanitizeForJSON(rv.MapIndex(key).Interface())
		}
		return out
	default:
		return v
	}
}

// sanitizeReport converts []TestRow into []map[string]any with NaN/Inf
// converted to nulls so the result is safe to encode to JSON.
func sanitizeReport(rows []TestRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		vals := make(map[string]any, len(r.Values))
		for k, fv := range r.Values {
			if math.IsNaN(fv) || math.IsInf(fv, 0) {
				vals[k] = nil
			} else {
				vals[k] = fv
			}
		}
		out = append(out, map[string]any{"name": r.Name, "values": vals, "status": r.Status})
	}
	return out
}

func bitsFromMultipart(r *http.Request, modeParam string) ([]int, error) {
	if r.MultipartForm == nil {
		if err := r.ParseMultipartForm(32 << 20); err != nil && !errors.Is(err, multipart.ErrMessageTooLarge) {
			return nil, err
		}
	}
	// Ищем поле file (или берём любой первый файл)
	var fh *multipart.FileHeader
	if r.MultipartForm != nil && r.MultipartForm.File != nil {
		if files := r.MultipartForm.File["file"]; len(files) > 0 {
			fh = files[0]
		} else {
			// любой первый файл
			for _, arr := range r.MultipartForm.File {
				if len(arr) > 0 {
					fh = arr[0]
					break
				}
			}
		}
	}
	if fh == nil {
		// возможно в обычном поле пришла строка bits
		if s := r.FormValue("bits"); s != "" {
			return AnalyzeBitsFromString(s)
		}
		return nil, errors.New("no file provided")
	}
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, f); err != nil {
		return nil, err
	}
	data := buf.Bytes()

	// определить режим
	mode := modeFromString(modeParam)
	if mode == -1 {
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		switch ext {
		case ".txt":
			mode = FileModeTXT
		case ".bin", ".dat", ".raw":
			mode = guessBinMode(data)
		default:
			// эвристика
			if looksLikeBitsString(string(data)) {
				mode = FileModeTXT
			} else {
				mode = guessBinMode(data)
			}
		}
	}

	switch mode {
	case FileModeTXT:
		return AnalyzeBitsFromString(string(data))
	case FileModeBinBytes01:
		return parseBinBytes01(data)
	case FileModeBinPackedMSB:
		return unpackBitsMSB(data), nil
	default:
		return nil, fmt.Errorf("unknown mode")
	}
}

func looksLikeBitsString(s string) bool {
	if s == "" {
		return false
	}
	count := 0
	for _, r := range s {
		if r == '0' || r == '1' {
			count++
		} else if unicode.IsSpace(r) {
			continue
		} else {
			return false
		}
	}
	return count > 0
}

func modeFromString(s string) FileMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "txt":
		return FileModeTXT
	case "bin01":
		return FileModeBinBytes01
	case "binpacked":
		return FileModeBinPackedMSB
	default:
		return -1
	}
}

/* ===========================
   (ОПЦ) Вспомогательный CLI
   =========================== */

// Не вызывается автоматически — оставлено на случай локальной проверки.
// go run . --string 0101...
func _maybeRunCLI(args []string) (bool, error) {
	if len(args) == 0 || (args[0] != "--string" && args[0] != "--input") {
		return false, nil
	}
	if args[0] == "--string" && len(args) >= 2 {
		bits, err := AnalyzeBitsFromString(args[1])
		if err != nil {
			return true, err
		}
		tests, report := ComputeAllTests(bits)
		out := map[string]any{"n": len(bits), "tests": tests, "report": report}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return true, enc.Encode(out)
	}
	if args[0] == "--input" && len(args) >= 3 {
		path := args[1]
		mode := modeFromString(args[2])
		raw, err := os.ReadFile(path)
		if err != nil {
			return true, err
		}
		var bits []int
		switch mode {
		case FileModeTXT:
			bits, err = AnalyzeBitsFromString(string(raw))
		case FileModeBinBytes01:
			bits, err = parseBinBytes01(raw)
		case FileModeBinPackedMSB:
			bits = unpackBitsMSB(raw)
		default:
			err = fmt.Errorf("unknown mode %s", args[2])
		}
		if err != nil {
			return true, err
		}
		tests, report := ComputeAllTests(bits)
		out := map[string]any{"n": len(bits), "tests": tests, "report": report}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return true, enc.Encode(out)
	}
	return true, fmt.Errorf("usage: --string <bits> | --input <path> <txt|bin01|binpacked>")
}
