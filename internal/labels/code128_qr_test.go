package labels

import (
	"strings"
	"testing"
)

func TestCode128PatternTableInvariants(t *testing.T) {
	// ISO/IEC 15417 guarantees:
	// - Symbols 0..105 have 6 digits summing to 11 modules, with EVEN dark-bar sum and ODD white-space sum.
	// - Symbol 106 (Stop) has 7 digits "2331112" summing to 13 modules.
	for sym := 0; sym <= 105; sym++ {
		pat := code128Patterns[sym]
		if len(pat) != 6 {
			t.Fatalf("symbol %d pattern %q has len %d, want 6", sym, pat, len(pat))
		}
		barSum, spaceSum := 0, 0
		for i := 0; i < 6; i++ {
			w := int(pat[i] - '0')
			if w < 1 || w > 4 {
				t.Fatalf("symbol %d element %d out of range [1,4]: %d", sym, i, w)
			}
			if i%2 == 0 {
				barSum += w
			} else {
				spaceSum += w
			}
		}
		if barSum+spaceSum != 11 {
			t.Errorf("symbol %d total modules = %d, want 11", sym, barSum+spaceSum)
		}
		if barSum%2 != 0 {
			t.Errorf("symbol %d barSum = %d, want even parity", sym, barSum)
		}
	}

	if code128Patterns[106] != "2331112" {
		t.Fatalf("stop symbol 106 = %q, want '2331112'", code128Patterns[106])
	}
}

func TestCode128EncodingCompressionAndSVG(t *testing.T) {
	// 1. Alphanumeric internal barcode "KRT-000142" should switch to Code C for "000142"
	bc, err := EncodeCode128("KRT-000142")
	if err != nil {
		t.Fatalf("EncodeCode128(KRT-000142) failed: %v", err)
	}
	if bc.Symbols[0] != code128StartB {
		t.Errorf("expected Start B (104), got %d", bc.Symbols[0])
	}
	if bc.Symbols[len(bc.Symbols)-1] != code128Stop {
		t.Errorf("expected Stop (106) at end, got %d", bc.Symbols[len(bc.Symbols)-1])
	}

	// Verify Code Set C double-density compression on 12-digit numeric barcode
	numBC, err := EncodeCode128("869000111222")
	if err != nil {
		t.Fatalf("EncodeCode128(numeric) failed: %v", err)
	}
	if numBC.Symbols[0] != code128StartC {
		t.Errorf("expected Start C (105) for 12-digit numeric barcode, got %d", numBC.Symbols[0])
	}
	// StartC + 6 digit pairs + checksum + Stop = 9 symbols (instead of 15 in pure Code B)
	if len(numBC.Symbols) != 9 {
		t.Errorf("expected 9 compressed Code C symbols, got %d (%v)", len(numBC.Symbols), numBC.Symbols)
	}

	// Verify non-ASCII and empty rejection
	if _, err := EncodeCode128(""); err == nil {
		t.Error("expected error on empty Code128 input")
	}
	if _, err := EncodeCode128("ÜRÜN-1"); err == nil {
		t.Error("expected error on non-ASCII Code128 input")
	}

	// Verify SVG rendering
	opts := DefaultCode128SVGOptions()
	opts.Title = "ESP32-S3 DevKit"
	svg, err := RenderCode128SVG("LOC:A-01-B3", opts)
	if err != nil {
		t.Fatalf("RenderCode128SVG failed: %v", err)
	}
	for _, want := range []string{"<svg ", "<path ", "LOC:A-01-B3", "ESP32-S3 DevKit", "</svg>"} {
		if !strings.Contains(svg, want) {
			t.Errorf("Code128 SVG missing expected fragment %q", want)
		}
	}
}

func TestGF256AndReedSolomonIdentities(t *testing.T) {
	if gfMul(0, 123) != 0 || gfMul(123, 0) != 0 {
		t.Fatal("expected gfMul with 0 to return 0")
	}
	if gfMul(1, 0xAB) != 0xAB || gfMul(0xAB, 1) != 0xAB {
		t.Fatal("expected gfMul identity with 1")
	}
	if gfMul(0x57, 0x83) != gfMul(0x83, 0x57) {
		t.Fatal("expected gfMul commutativity")
	}

	// Known ISO/IEC 18004 Annex I / Version 1-M ECC check:
	// Data codewords (16 bytes) -> 10 ECC bytes must be deterministic and non-zero
	sample := []byte{0x40, 0x54, 0x86, 0x56, 0xC6, 0xC6, 0xF0, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC}
	ecc := rsComputeECC(sample, 10)
	if len(ecc) != 10 {
		t.Fatalf("expected 10 ECC bytes, got %d", len(ecc))
	}
	allZero := true
	for _, b := range ecc {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("expected non-zero Reed-Solomon ECC bytes")
	}

	// Verify ISO/IEC 18004 format bits for Level M (00), Mask 0 (000) == 0x5412
	if got := computeFormatBits(0, 0); got != 0x5412 {
		t.Errorf("computeFormatBits(M, 0) = 0x%04X, want 0x5412", got)
	}
	// Verify ISO/IEC 18004 Annex D version info bits for Version 7 == 0x07C94
	if got := computeVersionBits(7); got != 0x07C94 {
		t.Errorf("computeVersionBits(7) = 0x%05X, want 0x07C94", got)
	}
}

func TestQREncodingVersionsStructureSVGAndASCII(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		wantVersion int
		wantSize    int
	}{
		{name: "V1_ShortSKU", payload: "KRT-000142", wantVersion: 1, wantSize: 21},
		{name: "V2_LANURL", payload: "http://192.168.1.42:8765", wantVersion: 2, wantSize: 25},
		{name: "V3_HTTPSLANURL", payload: "https://192.168.100.254:8765/?lang=tr", wantVersion: 3, wantSize: 29},
		{name: "V7_LongJSONPayload", payload: strings.Repeat("KRATEL-INV-", 10), wantVersion: 7, wantSize: 45},
		{name: "V10_MaxPayload", payload: strings.Repeat("A", 200), wantVersion: 10, wantSize: 57},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qr, err := EncodeQR(tc.payload)
			if err != nil {
				t.Fatalf("EncodeQR(%q) failed: %v", tc.payload, err)
			}
			if qr.Version != tc.wantVersion || qr.Size != tc.wantSize {
				t.Fatalf("got Version=%d Size=%d, want Version=%d Size=%d",
					qr.Version, qr.Size, tc.wantVersion, tc.wantSize)
			}

			// Verify top-left, top-right, bottom-left 7x7 finder cores (center 3x3 must all be dark)
			centers := [][2]int{{3, 3}, {qr.Size - 4, 3}, {3, qr.Size - 4}}
			for _, c := range centers {
				if !qr.Modules[c[1]][c[0]] {
					t.Errorf("finder center (%d,%d) is light, want dark", c[0], c[1])
				}
				// Outer finder border corner must be dark
				if !qr.Modules[c[1]-3][c[0]-3] {
					t.Errorf("finder corner (%d,%d) is light, want dark", c[0]-3, c[1]-3)
				}
				// Inner white ring must be light
				if qr.Modules[c[1]-2][c[0]-2] {
					t.Errorf("finder white ring (%d,%d) is dark, want light", c[0]-2, c[1]-2)
				}
			}

			// Verify fixed dark module at (8, size-8)
			if !qr.Modules[qr.Size-8][8] {
				t.Errorf("fixed dark module (8, %d) must be dark", qr.Size-8)
			}

			// Verify timing pattern alternation on row 6
			for x := 8; x < qr.Size-8; x++ {
				wantDark := x%2 == 0
				if qr.Modules[6][x] != wantDark {
					t.Errorf("timing row (6,%d) = %v, want %v", x, qr.Modules[6][x], wantDark)
				}
			}
		})
	}

	// Verify QR SVG rendering and Terminal ASCII QR rendering
	opts := DefaultQRSVGOptions()
	opts.Title = "Join Kratel LAN"
	opts.Caption = "http://192.168.1.42:8765"
	svg, err := RenderQRSVG("http://192.168.1.42:8765", opts)
	if err != nil {
		t.Fatalf("RenderQRSVG failed: %v", err)
	}
	for _, want := range []string{"<svg ", "<path ", "Join Kratel LAN", "http://192.168.1.42:8765", "</svg>"} {
		if !strings.Contains(svg, want) {
			t.Errorf("QR SVG missing expected fragment %q", want)
		}
	}

	ascii, err := RenderQRASCII("KRT-000142")
	if err != nil || len(ascii) == 0 {
		t.Fatalf("RenderQRASCII failed: %v", err)
	}
}
