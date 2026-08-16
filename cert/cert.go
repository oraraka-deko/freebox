package cert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"freebox/storage"
)

// KeyAlgorithm specifies the cryptographic algorithm for the key pair.
type KeyAlgorithm string

const (
	AlgRSA2048 KeyAlgorithm = "RSA2048"
	AlgRSA4096 KeyAlgorithm = "RSA4096"
	AlgECDSAP256 KeyAlgorithm = "ECDSA-P256"
	AlgECDSAP384 KeyAlgorithm = "ECDSA-P384"
	AlgEd25519   KeyAlgorithm = "Ed25519"
)

// CertificateOptions configures X.509 certificate generation.
type CertificateOptions struct {
	CommonName   string       `json:"common_name"`
	Organization string       `json:"organization,omitempty"`
	Country      string       `json:"country,omitempty"`
	Province     string       `json:"province,omitempty"`
	Locality     string       `json:"locality,omitempty"`
	ValidityDays int          `json:"validity_days"`
	IsCA         bool         `json:"is_ca"`
	Algorithm    KeyAlgorithm `json:"algorithm"`
	DNSNames     []string     `json:"dns_names,omitempty"`
	IPAddresses  []string     `json:"ip_addresses,omitempty"`
}

// CertificateBundle holds PEM-encoded certificate and private key strings.
type CertificateBundle struct {
	ID             string             `json:"id"`
	CommonName     string             `json:"common_name"`
	CertPEM        string             `json:"cert_pem"`
	KeyPEM         string             `json:"key_pem"`
	Algorithm      KeyAlgorithm       `json:"algorithm"`
	NotBefore      time.Time          `json:"not_before"`
	NotAfter       time.Time          `json:"not_after"`
	SerialNumber   string             `json:"serial_number"`
	IsCA           bool               `json:"is_ca"`
	DNSNames       []string           `json:"dns_names,omitempty"`
	IPAddresses    []string           `json:"ip_addresses,omitempty"`
	Config         CertificateOptions `json:"config"`
}

// DefaultOptions returns standard TLS server certificate parameters.
func DefaultOptions(commonName string) CertificateOptions {
	if commonName == "" {
		commonName = "localhost"
	}
	return CertificateOptions{
		CommonName:   commonName,
		Organization: "Freebox Secure",
		ValidityDays: 365,
		IsCA:         false,
		Algorithm:    AlgECDSAP256,
		DNSNames:     []string{commonName, "localhost", "*.local"},
		IPAddresses:  []string{"127.0.0.1", "::1"},
	}
}

// Generate creates a self-signed X.509 certificate and private key.
func Generate(opts CertificateOptions) (*CertificateBundle, error) {
	if opts.CommonName == "" {
		opts.CommonName = "localhost"
	}
	if opts.ValidityDays <= 0 {
		opts.ValidityDays = 365
	}
	if opts.Algorithm == "" {
		opts.Algorithm = AlgECDSAP256
	}

	privKey, pubKey, err := generateKeyPair(opts.Algorithm)
	if err != nil {
		return nil, fmt.Errorf("failed generating key pair: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed generating serial number: %w", err)
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(time.Duration(opts.ValidityDays) * 24 * time.Hour)

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   opts.CommonName,
			Organization: []string{opts.Organization},
			Country:      []string{opts.Country},
			Province:     []string{opts.Province},
			Locality:     []string{opts.Locality},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	if opts.IsCA {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		}
	}

	// Add DNS Names
	template.DNSNames = opts.DNSNames

	// Add IP Addresses
	for _, ipStr := range opts.IPAddresses {
		if ip := net.ParseIP(strings.TrimSpace(ipStr)); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		}
	}

	// Self-sign certificate
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, pubKey, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed creating certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	keyBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed marshaling private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})

	id := fmt.Sprintf("cert-%d", time.Now().UnixNano())

	return &CertificateBundle{
		ID:           id,
		CommonName:   opts.CommonName,
		CertPEM:      string(certPEM),
		KeyPEM:       string(keyPEM),
		Algorithm:    opts.Algorithm,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		SerialNumber: serialNumber.String(),
		IsCA:         opts.IsCA,
		DNSNames:     opts.DNSNames,
		IPAddresses:  opts.IPAddresses,
		Config:       opts,
	}, nil
}

// ToTLSConfig parses CertificateBundle into a ready-to-use tls.Config.
func (b *CertificateBundle) ToTLSConfig() (*tls.Config, error) {
	tlsCert, err := tls.X509KeyPair([]byte(b.CertPEM), []byte(b.KeyPEM))
	if err != nil {
		return nil, fmt.Errorf("invalid TLS key pair: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}, nil
}

func generateKeyPair(alg KeyAlgorithm) (crypto.PrivateKey, crypto.PublicKey, error) {
	switch alg {
	case AlgRSA2048:
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, nil, err
		}
		return k, &k.PublicKey, nil
	case AlgRSA4096:
		k, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			return nil, nil, err
		}
		return k, &k.PublicKey, nil
	case AlgECDSAP384:
		k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		return k, &k.PublicKey, nil
	case AlgEd25519:
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		return priv, pub, nil
	case AlgECDSAP256:
		fallthrough
	default:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		return k, &k.PublicKey, nil
	}
}

// Manager handles saving and retrieving certificates from database.
type Manager struct {
	db *storage.DB
}

// NewManager creates a new certificate manager.
func NewManager(db *storage.DB) *Manager {
	return &Manager{db: db}
}

// Save stores certificate bundle in database.
func (m *Manager) Save(b *CertificateBundle) error {
	if m.db == nil {
		return errors.New("database not available")
	}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return m.db.PutEncrypted(storage.BucketCertificates, b.ID, data)
}

// Get retrieves certificate bundle by ID.
func (m *Manager) Get(id string) (*CertificateBundle, error) {
	if m.db == nil {
		return nil, errors.New("database not available")
	}
	data, err := m.db.GetDecrypted(storage.BucketCertificates, id)
	if err != nil {
		return nil, err
	}
	var b CertificateBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// List returns all saved certificates.
func (m *Manager) List() ([]*CertificateBundle, error) {
	if m.db == nil {
		return nil, nil
	}
	raw, err := m.db.ListDecrypted(storage.BucketCertificates)
	if err != nil {
		return nil, err
	}
	var results []*CertificateBundle
	for _, data := range raw {
		var b CertificateBundle
		if err := json.Unmarshal(data, &b); err == nil {
			results = append(results, &b)
		}
	}
	return results, nil
}

// Delete removes certificate by ID.
func (m *Manager) Delete(id string) error {
	if m.db == nil {
		return errors.New("database not available")
	}
	return m.db.Delete(storage.BucketCertificates, id)
}
