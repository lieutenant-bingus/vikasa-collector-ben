package publish

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// privateCA writes a fresh CA to caFile and returns a server certificate it
// signed for 127.0.0.1 — the private CA a fleet runs (ADR 0022 decision 2).
func privateCA(t *testing.T) (caFile string, serverCert tls.Certificate) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test fleet CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	must(err)
	ca, err := x509.ParseCertificate(caDER)
	must(err)

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "cabinet broker"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:    x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, ca, &srvKey.PublicKey, caKey)
	must(err)

	caFile = filepath.Join(t.TempDir(), "ca.pem")
	must(os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644))
	return caFile, tls.Certificate{Certificate: [][]byte{srvDER}, PrivateKey: srvKey}
}

// A tls:// broker is verified against -nats-ca: with it the collector
// publishes; without it the system roots do not know the private CA, the
// connection is refused, and nothing in the collector skips verification.
func TestTLSBrokerIsVerifiedAgainstTheNamedCA(t *testing.T) {
	caFile, cert := privateCA(t)
	port := freePort(t)
	startNATSOn(t, port, func(o *server.Options) {
		o.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	})
	url := fmt.Sprintf("tls://127.0.0.1:%d", port)

	t.Run("with the CA", func(t *testing.T) {
		tune := fast(10)
		tune.security = Security{CAFile: caFile}
		p, err := start(t.Context(), url, testTemplate(t), []string{healthType}, quiet(), tune)
		if err != nil {
			t.Fatal(err)
		}
		defer drainBounded(p)
		if err := p.Enqueue(envN(1, nil), "cab-1"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "a publish over verified TLS", func() bool { return p.Stats().Published == 1 })
	})

	t.Run("without it", func(t *testing.T) {
		p, err := start(t.Context(), url, testTemplate(t), []string{healthType}, quiet(), fast(10))
		if err != nil {
			t.Fatal(err)
		}
		defer drainBounded(p)
		time.Sleep(300 * time.Millisecond) // several reconnect attempts at fast()'s 50ms
		if p.Connected() {
			t.Fatal("connected to a broker whose certificate no trusted root signed")
		}
	})
}
