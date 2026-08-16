package cert

import (
	"path/filepath"
	"testing"

	"freebox/storage"
)

func TestCertificateGeneration(t *testing.T) {
	algorithms := []KeyAlgorithm{AlgECDSAP256, AlgRSA2048, AlgEd25519}

	for _, alg := range algorithms {
		t.Run(string(alg), func(t *testing.T) {
			bundle, err := Generate(CertificateOptions{
				CommonName:   "freebox.local",
				Organization: "Freebox Test",
				ValidityDays: 90,
				Algorithm:    alg,
				DNSNames:     []string{"freebox.local", "localhost"},
				IPAddresses:  []string{"127.0.0.1"},
			})
			if err != nil {
				t.Fatalf("Generate failed for %s: %v", alg, err)
			}
			if bundle.CertPEM == "" || bundle.KeyPEM == "" {
				t.Errorf("empty cert/key PEM for %s", alg)
			}

			// Verify converting to TLS Config
			tlsCfg, err := bundle.ToTLSConfig()
			if err != nil {
				t.Fatalf("ToTLSConfig failed for %s: %v", alg, err)
			}
			if len(tlsCfg.Certificates) == 0 {
				t.Errorf("no tls certificates parsed for %s", alg)
			}
		})
	}
}

func TestCertificateManagerPersistence(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cert.db")
	db, err := storage.Open(storage.Config{
		Path:       dbPath,
		Passphrase: "test-passphrase",
	})
	if err != nil {
		t.Fatalf("failed to open storage db: %v", err)
	}
	defer db.Close()

	mgr := NewManager(db)
	bundle, err := Generate(DefaultOptions("test.server"))
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	err = mgr.Save(bundle)
	if err != nil {
		t.Fatalf("Save cert failed: %v", err)
	}

	retrieved, err := mgr.Get(bundle.ID)
	if err != nil {
		t.Fatalf("Get cert failed: %v", err)
	}
	if retrieved.CommonName != "test.server" {
		t.Errorf("CommonName mismatch: %s", retrieved.CommonName)
	}

	list, _ := mgr.List()
	if len(list) != 1 {
		t.Errorf("expected 1 cert in list, got %d", len(list))
	}

	_ = mgr.Delete(bundle.ID)
	listAfter, _ := mgr.List()
	if len(listAfter) != 0 {
		t.Errorf("expected 0 certs after delete")
	}
}
