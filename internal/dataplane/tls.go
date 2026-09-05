package dataplane

import (
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// TLSListener reloads the certificate files when a new TLS handshake observes
// a changed certificate or key modification time.
func TLSListener(listener net.Listener, certFile, keyFile string) (net.Listener, error) {
	loader := &certificateLoader{certFile: certFile, keyFile: keyFile}
	if _, err := loader.load(); err != nil {
		return nil, err
	}
	return tls.NewListener(listener, &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: loader.getCertificate,
	}), nil
}

type certificateLoader struct {
	certFile string
	keyFile  string
	mu       sync.Mutex
	cert     *tls.Certificate
	certMod  time.Time
	keyMod   time.Time
}

func (loader *certificateLoader) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return loader.load()
}

func (loader *certificateLoader) load() (*tls.Certificate, error) {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	certInfo, certErr := os.Stat(loader.certFile)
	keyInfo, keyErr := os.Stat(loader.keyFile)
	if certErr != nil || keyErr != nil {
		return nil, fmt.Errorf("stat TLS certificate files: %v %v", certErr, keyErr)
	}
	if loader.cert != nil && certInfo.ModTime().Equal(loader.certMod) && keyInfo.ModTime().Equal(loader.keyMod) {
		return loader.cert, nil
	}
	certificate, err := tls.LoadX509KeyPair(loader.certFile, loader.keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	loader.cert = &certificate
	loader.certMod = certInfo.ModTime()
	loader.keyMod = keyInfo.ModTime()
	return loader.cert, nil
}
