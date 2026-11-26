package labels

// Galois Field GF(2^8) tables for ISO/IEC 18004 QR Code Reed-Solomon error correction
// using primitive polynomial x^8 + x^4 + x^3 + x^2 + 1 (0x11D).
var (
	gfExp [512]byte
	gfLog [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

// gfMul multiplies two elements in GF(2^8).
func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsGeneratorPoly constructs the monic Reed-Solomon generator polynomial of the given degree:
// prod_{i=0}^{degree-1} (x - alpha^i).
// The returned slice has length degree+1, ordered from highest power to constant term.
func rsGeneratorPoly(degree int) []byte {
	poly := []byte{1}
	for i := 0; i < degree; i++ {
		next := make([]byte, len(poly)+1)
		factor := gfExp[i]
		for j := 0; j < len(poly); j++ {
			next[j] ^= poly[j]
			next[j+1] ^= gfMul(poly[j], factor)
		}
		poly = next
	}
	return poly
}

// rsComputeECC computes eccCount Reed-Solomon error correction codewords for data in GF(2^8).
func rsComputeECC(data []byte, eccCount int) []byte {
	gen := rsGeneratorPoly(eccCount)
	rem := make([]byte, eccCount)

	for _, b := range data {
		factor := b ^ rem[0]
		copy(rem, rem[1:])
		rem[eccCount-1] = 0
		if factor != 0 {
			for i := 0; i < eccCount; i++ {
				rem[i] ^= gfMul(gen[i+1], factor)
			}
		}
	}
	return rem
}
