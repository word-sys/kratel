package version

import (
	"fmt"
	"runtime"
	"strings"
)

const (
	// BrandName is the official product display name.
	BrandName = "Kratel"
	// PackageName is the canonical CLI binary and package identifier.
	PackageName = "kratel"
	// DefaultDBFile is the portable single-file SQLite ledger filename.
	DefaultDBFile = "kratel.db"
	// DefaultPort is the default local HTTP server port.
	DefaultPort = 8765
)

var (
	// Version is the semantic release version; overridable at link time via -ldflags.
	Version = "1.0.0"
	// Commit is the git revision hash; overridable at link time via -ldflags.
	Commit = "dev"
	// BuildDate is the RFC3339 build timestamp; overridable at link time via -ldflags.
	BuildDate = "source"
)

// Info holds immutable build and host runtime metadata for CLI and REST diagnostics.
type Info struct {
	Brand     string `json:"brand"`
	Package   string `json:"package"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Compiler  string `json:"compiler"`
	NumCPU    int    `json:"num_cpu"`
}

// Get returns the current build and runtime metadata snapshot.
func Get() Info {
	v := strings.TrimSpace(Version)
	if v == "" {
		v = "1.0.0"
	}
	c := strings.TrimSpace(Commit)
	if c == "" {
		c = "dev"
	}
	b := strings.TrimSpace(BuildDate)
	if b == "" {
		b = "source"
	}
	return Info{
		Brand:     BrandName,
		Package:   PackageName,
		Version:   v,
		Commit:    c,
		BuildDate: b,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Compiler:  runtime.Compiler,
		NumCPU:    runtime.NumCPU(),
	}
}

// String formats a human-readable CLI version line.
func (i Info) String() string {
	return fmt.Sprintf(
		"%s (%s) v%s (commit: %s, built: %s, %s, %s/%s)",
		i.Brand,
		i.Package,
		i.Version,
		i.Commit,
		i.BuildDate,
		i.GoVersion,
		i.OS,
		i.Arch,
	)
}

// UserAgent formats the canonical HTTP User-Agent string for webhooks and exports.
func (i Info) UserAgent() string {
	return fmt.Sprintf("%s/%s (%s/%s)", i.Package, i.Version, i.OS, i.Arch)
}
