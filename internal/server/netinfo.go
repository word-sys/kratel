package server

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/word-sys/kratel/internal/labels"
)

// NetworkInfo describes Kratel's active HTTP/HTTPS bind address, LAN URLs, and Join QR codes.
type NetworkInfo struct {
	BindAddr    string   `json:"bind_addr"`
	Port        int      `json:"port"`
	Scheme      string   `json:"scheme"`
	LANMode     bool     `json:"lan_mode"`
	TLSEnabled  bool     `json:"tls_enabled"`
	TLSFingerprint string `json:"tls_fingerprint,omitempty"`
	LocalURL    string   `json:"local_url"`
	JoinURL     string   `json:"join_url"`
	LANIPs      []string `json:"lan_ips"`
	LANURLs     []string `json:"lan_urls"`
	JoinQRSVG   string   `json:"join_qr_svg"`
	JoinQRASCII string   `json:"-"`
}

// DiscoverLANIPv4s inspects active network interfaces and returns non-loopback IPv4 addresses,
// prioritizing RFC 1918 private LAN addresses (192.168.x.x, 10.x.x.x, 172.16-31.x.x).
func DiscoverLANIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var ips []string

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			s := ip4.String()
			if !seen[s] {
				seen[s] = true
				ips = append(ips, s)
			}
		}
	}

	sort.Slice(ips, func(i, j int) bool {
		pi := lanIPPriority(ips[i])
		pj := lanIPPriority(ips[j])
		if pi != pj {
			return pi < pj
		}
		return ips[i] < ips[j]
	})

	return ips
}

func lanIPPriority(ip string) int {
	switch {
	case strings.HasPrefix(ip, "192.168."):
		return 0
	case strings.HasPrefix(ip, "10."):
		return 1
	case is172Private(ip):
		return 2
	default:
		return 3
	}
}

func is172Private(ip string) bool {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return false
	}
	return parsed[0] == 172 && parsed[1] >= 16 && parsed[1] <= 31
}

// BuildNetworkInfo constructs the local URL, LAN join URLs, and vector/ASCII QR codes for the active server.
func BuildNetworkInfo(bindAddr string, port int, tlsEnabled bool, tlsFingerprint string) NetworkInfo {
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}

	addr := strings.TrimSpace(bindAddr)
	if addr == "" {
		addr = "127.0.0.1"
	}
	lanMode := addr == "0.0.0.0" || addr == "::" || (addr != "127.0.0.1" && addr != "localhost" && addr != "::1")

	localHost := "127.0.0.1"
	if addr != "0.0.0.0" && addr != "::" {
		localHost = addr
	}
	localURL := fmt.Sprintf("%s://%s:%d", scheme, localHost, port)

	lanIPs := DiscoverLANIPv4s()
	lanURLs := make([]string, 0, len(lanIPs))
	for _, ip := range lanIPs {
		lanURLs = append(lanURLs, fmt.Sprintf("%s://%s:%d", scheme, ip, port))
	}

	joinURL := localURL
	if lanMode && len(lanURLs) > 0 {
		joinURL = lanURLs[0]
	}

	qrOpts := labels.DefaultQRSVGOptions()
	qrOpts.ModuleSize = 5.0
	qrOpts.Title = "Kratel LAN Access"
	qrOpts.Caption = joinURL
	qrSVG, _ := labels.RenderQRSVG(joinURL, qrOpts)
	qrASCII, _ := labels.RenderQRASCII(joinURL)

	return NetworkInfo{
		BindAddr:       addr,
		Port:           port,
		Scheme:         scheme,
		LANMode:        lanMode,
		TLSEnabled:     tlsEnabled,
		TLSFingerprint: tlsFingerprint,
		LocalURL:       localURL,
		JoinURL:        joinURL,
		LANIPs:         lanIPs,
		LANURLs:        lanURLs,
		JoinQRSVG:      qrSVG,
		JoinQRASCII:    qrASCII,
	}
}
