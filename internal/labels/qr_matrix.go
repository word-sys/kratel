package labels

import (
	"fmt"
	"math/bits"
	"strings"

	"github.com/word-sys/kratel/internal/apperr"
)

// QRCode holds a finalized ISO/IEC 18004 QR Code matrix (Error Correction Level M).
type QRCode struct {
	Text    string
	Version int
	Mask    int
	Size    int
	Modules [][]bool // Modules[y][x] == true indicates a dark module
}

type qrVersionSpec struct {
	version        int
	totalCodewords int
	dataCodewords  int
	eccPerBlock    int
	group1Blocks   int
	group1Data     int
	group2Blocks   int
	group2Data     int
	alignments     []int
}

// ISO/IEC 18004 Table 9 specifications for Error Correction Level M, Versions 1..10.
var qrLevelMSpecs = []qrVersionSpec{
	{version: 1, totalCodewords: 26, dataCodewords: 16, eccPerBlock: 10, group1Blocks: 1, group1Data: 16, alignments: nil},
	{version: 2, totalCodewords: 44, dataCodewords: 28, eccPerBlock: 16, group1Blocks: 1, group1Data: 28, alignments: []int{6, 18}},
	{version: 3, totalCodewords: 70, dataCodewords: 44, eccPerBlock: 26, group1Blocks: 1, group1Data: 44, alignments: []int{6, 22}},
	{version: 4, totalCodewords: 100, dataCodewords: 64, eccPerBlock: 18, group1Blocks: 2, group1Data: 32, alignments: []int{6, 26}},
	{version: 5, totalCodewords: 134, dataCodewords: 86, eccPerBlock: 24, group1Blocks: 2, group1Data: 43, alignments: []int{6, 30}},
	{version: 6, totalCodewords: 172, dataCodewords: 108, eccPerBlock: 16, group1Blocks: 4, group1Data: 27, alignments: []int{6, 34}},
	{version: 7, totalCodewords: 196, dataCodewords: 124, eccPerBlock: 18, group1Blocks: 4, group1Data: 31, alignments: []int{6, 22, 38}},
	{version: 8, totalCodewords: 242, dataCodewords: 154, eccPerBlock: 22, group1Blocks: 2, group1Data: 38, group2Blocks: 2, group2Data: 39, alignments: []int{6, 24, 42}},
	{version: 9, totalCodewords: 292, dataCodewords: 182, eccPerBlock: 22, group1Blocks: 3, group1Data: 36, group2Blocks: 2, group2Data: 37, alignments: []int{6, 26, 46}},
	{version: 10, totalCodewords: 346, dataCodewords: 216, eccPerBlock: 26, group1Blocks: 4, group1Data: 43, group2Blocks: 1, group2Data: 44, alignments: []int{6, 28, 50}},
}

// EncodeQR encodes arbitrary UTF-8 bytes into an ISO/IEC 18004 QR Code (Byte mode, Level M).
// Supports payloads up to 213 bytes (Versions 1 through 10), covering all SKUs, barcodes,
// bin labels, kit codes, and LAN join URLs.
func EncodeQR(text string) (*QRCode, error) {
	payload := []byte(text)
	if len(payload) == 0 {
		return nil, apperr.Validation("code", "QR code content cannot be empty")
	}

	spec, err := selectQRVersion(len(payload))
	if err != nil {
		return nil, err
	}

	dataCodewords := buildQRDataCodewords(payload, spec)
	finalCodewords := interleaveBlocksAndECC(dataCodewords, spec)

	size := spec.version*4 + 17
	baseMatrix := makeBoolGrid(size)
	isFunc := makeBoolGrid(size)

	placeFunctionPatterns(baseMatrix, isFunc, spec)
	placeDataBits(baseMatrix, isFunc, finalCodewords)

	// Evaluate all 8 ISO/IEC 18004 mask patterns and pick the lowest penalty score
	bestMask := 0
	bestPenalty := int(^uint(0) >> 1)
	var bestMatrix [][]bool

	for mask := 0; mask < 8; mask++ {
		cand := cloneBoolGrid(baseMatrix)
		applyQRMask(cand, isFunc, mask)
		placeFormatInfo(cand, 0, mask) // EC Level M == 0b00
		if spec.version >= 7 {
			placeVersionInfo(cand, spec.version)
		}
		pen := evaluateQRPenalty(cand)
		if pen < bestPenalty {
			bestPenalty = pen
			bestMask = mask
			bestMatrix = cand
		}
	}

	return &QRCode{
		Text:    text,
		Version: spec.version,
		Mask:    bestMask,
		Size:    size,
		Modules: bestMatrix,
	}, nil
}

func selectQRVersion(byteLen int) (qrVersionSpec, error) {
	for _, spec := range qrLevelMSpecs {
		countBits := 8
		if spec.version >= 10 {
			countBits = 16
		}
		requiredBits := 4 + countBits + byteLen*8
		if requiredBits <= spec.dataCodewords*8 {
			return spec, nil
		}
	}
	return qrVersionSpec{}, apperr.Validation("code", fmt.Sprintf("QR payload length (%d bytes) exceeds Version 10-M capacity (213 bytes)", byteLen))
}

func buildQRDataCodewords(payload []byte, spec qrVersionSpec) []byte {
	totalBits := spec.dataCodewords * 8
	buf := make([]byte, 0, totalBits)

	appendBits := func(val, count int) {
		for i := count - 1; i >= 0; i-- {
			buf = append(buf, byte((val>>i)&1))
		}
	}

	// Byte mode indicator: 0100
	appendBits(0b0100, 4)

	// Character count indicator (8 bits for V1-V9, 16 bits for V10+)
	if spec.version >= 10 {
		appendBits(len(payload), 16)
	} else {
		appendBits(len(payload), 8)
	}

	for _, b := range payload {
		appendBits(int(b), 8)
	}

	// Terminator: up to 4 zero bits
	term := totalBits - len(buf)
	if term > 4 {
		term = 4
	}
	for i := 0; i < term; i++ {
		buf = append(buf, 0)
	}

	// Pad to byte boundary
	for len(buf)%8 != 0 {
		buf = append(buf, 0)
	}

	codewords := make([]byte, 0, spec.dataCodewords)
	for i := 0; i < len(buf); i += 8 {
		var cw byte
		for b := 0; b < 8; b++ {
			cw = (cw << 1) | buf[i+b]
		}
		codewords = append(codewords, cw)
	}

	// Pad remaining capacity with alternating 0xEC, 0x11 codewords per ISO/IEC 18004
	padBytes := [2]byte{0xEC, 0x11}
	for p := 0; len(codewords) < spec.dataCodewords; p++ {
		codewords = append(codewords, padBytes[p%2])
	}
	return codewords
}

func interleaveBlocksAndECC(data []byte, spec qrVersionSpec) []byte {
	totalBlocks := spec.group1Blocks + spec.group2Blocks
	dataBlocks := make([][]byte, 0, totalBlocks)
	eccBlocks := make([][]byte, 0, totalBlocks)

	offset := 0
	for i := 0; i < spec.group1Blocks; i++ {
		blk := data[offset : offset+spec.group1Data]
		offset += spec.group1Data
		dataBlocks = append(dataBlocks, blk)
		eccBlocks = append(eccBlocks, rsComputeECC(blk, spec.eccPerBlock))
	}
	for i := 0; i < spec.group2Blocks; i++ {
		blk := data[offset : offset+spec.group2Data]
		offset += spec.group2Data
		dataBlocks = append(dataBlocks, blk)
		eccBlocks = append(eccBlocks, rsComputeECC(blk, spec.eccPerBlock))
	}

	out := make([]byte, 0, spec.totalCodewords)
	maxDataLen := spec.group1Data
	if spec.group2Data > maxDataLen {
		maxDataLen = spec.group2Data
	}

	for col := 0; col < maxDataLen; col++ {
		for _, blk := range dataBlocks {
			if col < len(blk) {
				out = append(out, blk[col])
			}
		}
	}
	for col := 0; col < spec.eccPerBlock; col++ {
		for _, ecc := range eccBlocks {
			out = append(out, ecc[col])
		}
	}
	return out
}

func placeFunctionPatterns(m, fn [][]bool, spec qrVersionSpec) {
	size := len(m)

	// 1. Three 7x7 finder patterns + 1-module white separators
	placeFinder(m, fn, 0, 0)
	placeFinder(m, fn, size-7, 0)
	placeFinder(m, fn, 0, size-7)

	// 2. Timing patterns (row 6 and column 6)
	for i := 8; i < size-8; i++ {
		dark := i%2 == 0
		m[6][i] = dark
		fn[6][i] = true
		m[i][6] = dark
		fn[i][6] = true
	}

	// 3. Alignment patterns (5x5 squares) skipping overlaps with finder patterns
	for _, cy := range spec.alignments {
		for _, cx := range spec.alignments {
			if isFinderOverlap(cx, cy, size) {
				continue
			}
			placeAlignment(m, fn, cx, cy)
		}
	}

	// 4. Reserve format information modules around finders + fixed dark module
	for i := 0; i <= 8; i++ {
		if i != 6 {
			fn[8][i] = true
			fn[i][8] = true
		}
	}
	for i := size - 8; i < size; i++ {
		fn[8][i] = true
		fn[i][8] = true
	}
	// Fixed dark module at (8, 4*version + 9) == (8, size - 8)
	m[size-8][8] = true
	fn[size-8][8] = true

	// 5. Reserve version information areas for Version >= 7
	if spec.version >= 7 {
		for i := 0; i < 6; i++ {
			for j := 0; j < 3; j++ {
				fn[i][size-11+j] = true
				fn[size-11+j][i] = true
			}
		}
	}
}

func placeFinder(m, fn [][]bool, x0, y0 int) {
	size := len(m)
	for dy := -1; dy <= 7; dy++ {
		for dx := -1; dx <= 7; dx++ {
			x, y := x0+dx, y0+dy
			if x < 0 || x >= size || y < 0 || y >= size {
				continue
			}
			fn[y][x] = true
			if dx == -1 || dx == 7 || dy == -1 || dy == 7 {
				m[y][x] = false
				continue
			}
			onOuter := dx == 0 || dx == 6 || dy == 0 || dy == 6
			inCenter := dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4
			m[y][x] = onOuter || inCenter
		}
	}
}

func isFinderOverlap(cx, cy, size int) bool {
	if cx <= 8 && cy <= 8 {
		return true
	}
	if cx >= size-9 && cy <= 8 {
		return true
	}
	if cx <= 8 && cy >= size-9 {
		return true
	}
	return false
}

func placeAlignment(m, fn [][]bool, cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			x, y := cx+dx, cy+dy
			fn[y][x] = true
			dist := dx
			if dist < 0 {
				dist = -dist
			}
			dyAbs := dy
			if dyAbs < 0 {
				dyAbs = -dyAbs
			}
			if dyAbs > dist {
				dist = dyAbs
			}
			m[y][x] = dist == 0 || dist == 2
		}
	}
}

func placeDataBits(m, fn [][]bool, codewords []byte) {
	size := len(m)
	bitIdx := 0
	totalBits := len(codewords) * 8

	upward := true
	for right := size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for step := 0; step < size; step++ {
			y := step
			if upward {
				y = size - 1 - step
			}
			for colOffset := 0; colOffset < 2; colOffset++ {
				x := right - colOffset
				if fn[y][x] {
					continue
				}
				var bit bool
				if bitIdx < totalBits {
					cw := codewords[bitIdx/8]
					bit = ((cw >> (7 - uint(bitIdx%8))) & 1) != 0
					bitIdx++
				}
				m[y][x] = bit
			}
		}
		upward = !upward
	}
}

func applyQRMask(m, fn [][]bool, mask int) {
	size := len(m)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if fn[y][x] {
				continue
			}
			var invert bool
			switch mask {
			case 0:
				invert = (y+x)%2 == 0
			case 1:
				invert = y%2 == 0
			case 2:
				invert = x%3 == 0
			case 3:
				invert = (y+x)%3 == 0
			case 4:
				invert = (y/2+x/3)%2 == 0
			case 5:
				invert = (y*x)%2+(y*x)%3 == 0
			case 6:
				invert = ((y*x)%2+(y*x)%3)%2 == 0
			case 7:
				invert = ((y+x)%2+(y*x)%3)%2 == 0
			}
			if invert {
				m[y][x] = !m[y][x]
			}
		}
	}
}

func computeFormatBits(ecLevelBits, mask int) int {
	data := (ecLevelBits << 3) | mask
	rem := data << 10
	const poly = 0x537
	for bits.Len(uint(rem)) >= 11 {
		rem ^= poly << (bits.Len(uint(rem)) - 11)
	}
	return ((data << 10) | rem) ^ 0x5412
}

func placeFormatInfo(m [][]bool, ecLevelBits, mask int) {
	size := len(m)
	fBits := computeFormatBits(ecLevelBits, mask)

	bitAt := func(idx int) bool {
		return ((fBits >> uint(idx)) & 1) != 0
	}

	// Top-left finder horizontal (bits 14..8) and vertical (bits 7..0)
	for i := 0; i <= 5; i++ {
		m[8][i] = bitAt(14 - i)
	}
	m[8][7] = bitAt(8)
	m[8][8] = bitAt(7)
	m[7][8] = bitAt(6)
	for i := 0; i <= 5; i++ {
		m[5-i][8] = bitAt(5 - i)
	}

	// Bottom-left vertical (bits 14..8) and top-right horizontal (bits 7..0)
	for i := 0; i < 7; i++ {
		m[size-1-i][8] = bitAt(14 - i)
	}
	for i := 0; i < 8; i++ {
		m[8][size-8+i] = bitAt(7 - i)
	}
}

func computeVersionBits(version int) int {
	rem := version << 12
	const poly = 0x1F25
	for bits.Len(uint(rem)) >= 13 {
		rem ^= poly << (bits.Len(uint(rem)) - 13)
	}
	return (version << 12) | rem
}

func placeVersionInfo(m [][]bool, version int) {
	size := len(m)
	vBits := computeVersionBits(version)
	for i := 0; i < 18; i++ {
		dark := ((vBits >> uint(i)) & 1) != 0
		row := i / 3
		col := size - 11 + (i % 3)
		m[row][col] = dark
		m[col][row] = dark
	}
}

func evaluateQRPenalty(m [][]bool) int {
	size := len(m)
	penalty := 0

	// Rule 1: Runs of 5 or more same-colored modules in rows and columns
	for y := 0; y < size; y++ {
		runColor := m[y][0]
		runLen := 1
		for x := 1; x < size; x++ {
			if m[y][x] == runColor {
				runLen++
			} else {
				if runLen >= 5 {
					penalty += 3 + (runLen - 5)
				}
				runColor = m[y][x]
				runLen = 1
			}
		}
		if runLen >= 5 {
			penalty += 3 + (runLen - 5)
		}
	}
	for x := 0; x < size; x++ {
		runColor := m[0][x]
		runLen := 1
		for y := 1; y < size; y++ {
			if m[y][x] == runColor {
				runLen++
			} else {
				if runLen >= 5 {
					penalty += 3 + (runLen - 5)
				}
				runColor = m[y][x]
				runLen = 1
			}
		}
		if runLen >= 5 {
			penalty += 3 + (runLen - 5)
		}
	}

	// Rule 2: 2x2 solid blocks
	for y := 0; y < size-1; y++ {
		for x := 0; x < size-1; x++ {
			c := m[y][x]
			if m[y][x+1] == c && m[y+1][x] == c && m[y+1][x+1] == c {
				penalty += 3
			}
		}
	}

	// Rule 3: Finder-like 1:1:3:1:1 patterns (with 4 light modules on either side)
	patA := [11]bool{true, false, true, true, true, false, true, false, false, false, false}
	patB := [11]bool{false, false, false, false, true, false, true, true, true, false, true}
	for y := 0; y < size; y++ {
		for x := 0; x <= size-11; x++ {
			if matchesRowPattern(m, y, x, patA) || matchesRowPattern(m, y, x, patB) {
				penalty += 40
			}
		}
	}
	for x := 0; x < size; x++ {
		for y := 0; y <= size-11; y++ {
			if matchesColPattern(m, x, y, patA) || matchesColPattern(m, x, y, patB) {
				penalty += 40
			}
		}
	}

	// Rule 4: Balance of dark modules vs 50%
	dark := 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if m[y][x] {
				dark++
			}
		}
	}
	total := size * size
	pct := (dark * 100) / total
	diff := pct - 50
	if diff < 0 {
		diff = -diff
	}
	penalty += (diff / 5) * 10

	return penalty
}

func matchesRowPattern(m [][]bool, y, x int, pat [11]bool) bool {
	for i := 0; i < 11; i++ {
		if m[y][x+i] != pat[i] {
			return false
		}
	}
	return true
}

func matchesColPattern(m [][]bool, x, y int, pat [11]bool) bool {
	for i := 0; i < 11; i++ {
		if m[y+i][x] != pat[i] {
			return false
		}
	}
	return true
}

func makeBoolGrid(size int) [][]bool {
	g := make([][]bool, size)
	for i := range g {
		g[i] = make([]bool, size)
	}
	return g
}

func cloneBoolGrid(src [][]bool) [][]bool {
	dst := make([][]bool, len(src))
	for i := range src {
		dst[i] = make([]bool, len(src[i]))
		copy(dst[i], src[i])
	}
	return dst
}

// RenderQRASCII renders a compact, smartphone-scannable terminal QR code using Unicode
// upper/lower half-block characters (▀, ▄, █, space) for --serve LAN join banners.
func RenderQRASCII(text string) (string, error) {
	qr, err := EncodeQR(text)
	if err != nil {
		return "", err
	}

	const quiet = 2
	full := qr.Size + 2*quiet
	isDark := func(x, y int) bool {
		qx := x - quiet
		qy := y - quiet
		if qx < 0 || qy < 0 || qx >= qr.Size || qy >= qr.Size {
			return false
		}
		return qr.Modules[qy][qx]
	}

	var sb strings.Builder
	for y := 0; y < full; y += 2 {
		for x := 0; x < full; x++ {
			top := isDark(x, y)
			bottom := y+1 < full && isDark(x, y+1)
			switch {
			case top && bottom:
				sb.WriteRune(' ')
			case top && !bottom:
				sb.WriteRune('▄')
			case !top && bottom:
				sb.WriteRune('▀')
			default:
				sb.WriteRune('█')
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}
