package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// GenerateSelfSignedTLSConfig creates an in-memory ECDSA P-256 TLS certificate covering
// localhost, 127.0.0.1, ::1, and all detected/supplied LAN IPv4 addresses so mobile browsers
// grant getUserMedia camera access over LAN HTTPS (--serve --tls-auto).
func GenerateSelfSignedTLSConfig(extraIPs []string) (*tls.Config, string, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate ECDSA P-256 key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, "", fmt.Errorf("generate certificate serial: %w", err)
	}

	now := time.Now().UTC()
	ipSet := map[string]net.IP{
		"127.0.0.1": net.ParseIP("127.0.0.1"),
		"::1":       net.ParseIP("::1"),
	}
	for _, raw := range append(DiscoverLANIPv4s(), extraIPs...) {
		if parsed := net.ParseIP(strings.TrimSpace(raw)); parsed != nil {
			ipSet[parsed.String()] = parsed
		}
	}

	ipAddrs := make([]net.IP, 0, len(ipSet))
	for _, ip := range ipSet {
		ipAddrs = append(ipAddrs, ip)
	}

	tmpl := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Kratel Local Workshop"},
			CommonName:   "kratel.local",
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(825 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "kratel.local"},
		IPAddresses:           ipAddrs,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, "", fmt.Errorf("create self-signed X.509 certificate: %w", err)
	}

	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, "", fmt.Errorf("marshal EC private key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, "", fmt.Errorf("load X.509 keypair: %w", err)
	}

	sum := sha256.Sum256(derBytes)
	fingerprint := formatFingerprint(sum[:])

	cfg := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	}
	return cfg, fingerprint, nil
}

func formatFingerprint(digest []byte) string {
	parts := make([]string, len(digest))
	for i, b := range digest {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}
