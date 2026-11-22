package version

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestGetAndFormatting(t *testing.T) {
	info := Get()

	if info.Brand != "Kratel" {
		t.Fatalf("expected Brand 'Kratel', got %q", info.Brand)
	}
	if info.Package != "kratel" {
		t.Fatalf("expected Package 'kratel', got %q", info.Package)
	}
	if info.Version == "" {
		t.Fatal("expected non-empty Version")
	}
	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Fatalf("expected OS/Arch %s/%s, got %s/%s", runtime.GOOS, runtime.GOARCH, info.OS, info.Arch)
	}
	if info.NumCPU < 1 {
		t.Fatalf("expected NumCPU >= 1, got %d", info.NumCPU)
	}

	line := info.String()
	for _, want := range []string{"Kratel (kratel)", "v" + info.Version, runtime.GOOS + "/" + runtime.GOARCH} {
		if !strings.Contains(line, want) {
			t.Errorf("expected String() %q to contain %q", line, want)
		}
	}

	ua := info.UserAgent()
	wantUA := "kratel/" + info.Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
	if ua != wantUA {
		t.Errorf("expected UserAgent() %q, got %q", wantUA, ua)
	}

	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal version Info: %v", err)
	}
	var decoded Info
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("failed to unmarshal version Info: %v", err)
	}
	if decoded != info {
		t.Errorf("round-trip JSON mismatch: got %+v, want %+v", decoded, info)
	}
}

func TestEmptyOverridesFallback(t *testing.T) {
	origV, origC, origB := Version, Commit, BuildDate
	defer func() {
		Version, Commit, BuildDate = origV, origC, origB
	}()

	Version = "   "
	Commit = ""
	BuildDate = ""

	info := Get()
	if info.Version != "1.0.0" {
		t.Errorf("expected fallback Version '1.0.0', got %q", info.Version)
	}
	if info.Commit != "dev" {
		t.Errorf("expected fallback Commit 'dev', got %q", info.Commit)
	}
	if info.BuildDate != "source" {
		t.Errorf("expected fallback BuildDate 'source', got %q", info.BuildDate)
	}
}
