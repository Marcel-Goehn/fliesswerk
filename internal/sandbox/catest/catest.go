// Package catest is what the integration tests of corporate proxy support
// share: a throwaway root CA and a proxy that intercepts TLS with it, as
// Zscaler does. Nothing that ships imports it.
package catest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"time"
)

// Answer is what the proxy answers every intercepted request with.
const Answer = "intercepted"

// CA is a root certificate and its key.
type CA struct {
	Cert            *x509.Certificate
	CertPEM, KeyPEM []byte
	key             *ecdsa.PrivateKey
}

// NewCA makes a root called name.
func NewCA(name string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{
		Cert: cert, key: key,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

// LoadCA is a CA from the PEM NewCA made.
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, errors.New("not PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, CertPEM: certPEM, KeyPEM: keyPEM, key: key}, nil
}

// leaf is a certificate for host, issued by the CA.
func (c *CA) leaf(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		DNSNames: []string{host}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, c.Cert.Raw}, PrivateKey: key}, nil
}

// Intercept answers every CONNECT on l itself, with a certificate from the
// CA for whichever host the client asked for. It returns when l closes.
func (c *CA) Intercept(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go c.serve(conn)
	}
}

func (c *CA) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil || req.Method != http.MethodConnect {
		return
	}
	if _, err := fmt.Fprint(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	server := tls.Server(conn, &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return c.leaf(hello.ServerName)
		},
	})
	if _, err := http.ReadRequest(bufio.NewReader(server)); err != nil {
		return
	}
	_, _ = fmt.Fprintf(server, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(Answer), Answer)
}
