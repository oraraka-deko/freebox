package signer

import (
	"archive/zip"
	"bytes"
	"testing"

	"freebox/cert"
	"freebox/vfs"
)

func TestDetachedSigningAndVerification(t *testing.T) {
	fs := vfs.NewMemFS()
	_ = fs.Write("/release/app-v1.0.tar.gz", []byte("Binary payload of release application"))

	bundle, err := cert.Generate(cert.CertificateOptions{
		CommonName:   "developer@freebox",
		Algorithm:    cert.AlgECDSAP256,
		ValidityDays: 365,
	})
	if err != nil {
		t.Fatalf("failed generating cert: %v", err)
	}

	// 1. Sign detached
	res, err := SignDetached(fs, "/release/app-v1.0.tar.gz", bundle.KeyPEM)
	if err != nil {
		t.Fatalf("SignDetached failed: %v", err)
	}
	if res.SignatureHex == "" || res.SignatureB64 == "" {
		t.Errorf("empty signature output")
	}

	// 2. Verify detached
	valid, err := VerifyDetached(fs, "/release/app-v1.0.tar.gz", res.SignatureHex, bundle.CertPEM)
	if err != nil || !valid {
		t.Fatalf("VerifyDetached failed: %v, valid=%v", err, valid)
	}

	// 3. Corrupt file and verify fails
	_ = fs.Write("/release/app-v1.0.tar.gz", []byte("Tampered data!"))
	validCorrupt, _ := VerifyDetached(fs, "/release/app-v1.0.tar.gz", res.SignatureHex, bundle.CertPEM)
	if validCorrupt {
		t.Errorf("expected signature verification to fail on tampered content")
	}
}

func TestAPKSigning(t *testing.T) {
	fs := vfs.NewMemFS()

	// Create sample unsigned APK/ZIP
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("classes.dex")
	_, _ = w.Write([]byte("dalvik bytecode simulation"))
	w, _ = zw.Create("AndroidManifest.xml")
	_, _ = w.Write([]byte("<manifest package=\"com.freebox.app\"/>"))
	_ = zw.Close()

	_ = fs.Write("/build/unsigned.apk", buf.Bytes())

	bundle, err := cert.Generate(cert.CertificateOptions{
		CommonName:   "Android Signer",
		Algorithm:    cert.AlgRSA2048,
		ValidityDays: 365,
	})
	if err != nil {
		t.Fatalf("cert generation failed: %v", err)
	}

	res, err := SignAPK(fs, "/build/unsigned.apk", "/build/signed.apk", bundle.CertPEM, bundle.KeyPEM)
	if err != nil {
		t.Fatalf("SignAPK failed: %v", err)
	}
	if !res.IsAPK || res.SignatureHex == "" {
		t.Errorf("invalid APK sign result: %+v", res)
	}

	// Verify signed APK contents
	signedData, err := fs.Read("/build/signed.apk")
	if err != nil {
		t.Fatalf("failed reading signed APK: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(signedData), int64(len(signedData)))
	if err != nil {
		t.Fatalf("invalid zip: %v", err)
	}

	hasManifest := false
	hasSF := false
	hasRSA := false
	for _, f := range zr.File {
		if f.Name == "META-INF/MANIFEST.MF" {
			hasManifest = true
		}
		if f.Name == "META-INF/CERT.SF" {
			hasSF = true
		}
		if f.Name == "META-INF/CERT.RSA" {
			hasRSA = true
		}
	}

	if !hasManifest || !hasSF || !hasRSA {
		t.Errorf("signed APK missing signature files: manifest=%v, sf=%v, rsa=%v", hasManifest, hasSF, hasRSA)
	}
}
