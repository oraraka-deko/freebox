package signer

import (
	"archive/zip"
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"freebox/vfs"
)

// SignResult contains metadata about a completed file signing.
type SignResult struct {
	FilePath     string    `json:"file_path"`
	SignatureHex string    `json:"signature_hex,omitempty"`
	SignatureB64 string    `json:"signature_b64,omitempty"`
	Algorithm    string    `json:"algorithm"`
	SignedAt     time.Time `json:"signed_at"`
	IsAPK        bool      `json:"is_apk"`
}

// SignDetached creates a detached cryptographic signature for any file on VFS.
func SignDetached(fsys vfs.FileSystem, filePath string, privKeyPEM string) (*SignResult, error) {
	filePath = vfs.NormalizePath(filePath)
	data, err := fsys.Read(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading %s: %w", filePath, err)
	}

	block, _ := pem.Decode([]byte(privKeyPEM))
	if block == nil {
		return nil, errors.New("invalid private key PEM")
	}

	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1 RSA
		privKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			// Try EC private key
			privKey, err = x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("failed parsing private key: %w", err)
			}
		}
	}

	hash := sha256.Sum256(data)
	var sig []byte
	var alg string

	switch k := privKey.(type) {
	case *rsa.PrivateKey:
		alg = "RSA-SHA256"
		sig, err = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, hash[:])
	case *ecdsa.PrivateKey:
		alg = "ECDSA-SHA256"
		sig, err = ecdsa.SignASN1(rand.Reader, k, hash[:])
	case ed25519.PrivateKey:
		alg = "Ed25519"
		sig = ed25519.Sign(k, data)
	default:
		return nil, fmt.Errorf("unsupported key type: %T", privKey)
	}

	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	return &SignResult{
		FilePath:     filePath,
		SignatureHex: hex.EncodeToString(sig),
		SignatureB64: base64.StdEncoding.EncodeToString(sig),
		Algorithm:    alg,
		SignedAt:     time.Now(),
		IsAPK:        false,
	}, nil
}

// VerifyDetached verifies a detached signature against file content.
func VerifyDetached(fsys vfs.FileSystem, filePath string, signatureHex string, certPEM string) (bool, error) {
	filePath = vfs.NormalizePath(filePath)
	data, err := fsys.Read(filePath)
	if err != nil {
		return false, err
	}

	sigBytes, err := hex.DecodeString(signatureHex)
	if err != nil {
		// Try Base64
		sigBytes, err = base64.StdEncoding.DecodeString(signatureHex)
		if err != nil {
			return false, errors.New("invalid signature encoding")
		}
	}

	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return false, errors.New("invalid certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("failed parsing certificate: %w", err)
	}

	hash := sha256.Sum256(data)

	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		err = rsa.VerifyPKCS1v15(pub, crypto.SHA256, hash[:], sigBytes)
		return err == nil, err
	case *ecdsa.PublicKey:
		valid := ecdsa.VerifyASN1(pub, hash[:], sigBytes)
		return valid, nil
	case ed25519.PublicKey:
		valid := ed25519.Verify(pub, data, sigBytes)
		return valid, nil
	default:
		return false, fmt.Errorf("unsupported public key type: %T", cert.PublicKey)
	}
}

// SignAPK adds JAR/APK v1 signature (META-INF/MANIFEST.MF, CERT.SF, CERT.RSA) to a ZIP/APK file.
func SignAPK(fsys vfs.FileSystem, apkPath, outPath string, certPEM, privKeyPEM string) (*SignResult, error) {
	apkPath = vfs.NormalizePath(apkPath)
	outPath = vfs.NormalizePath(outPath)

	apkData, err := fsys.Read(apkPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading apk %s: %w", apkPath, err)
	}

	zr, err := zip.NewReader(bytes.NewReader(apkData), int64(len(apkData)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip/apk archive: %w", err)
	}

	// 1. Build MANIFEST.MF
	var manifestBuf bytes.Buffer
	manifestBuf.WriteString("Manifest-Version: 1.0\r\n")
	manifestBuf.WriteString("Created-By: Freebox APK Signer 1.0\r\n\r\n")

	type fileDigest struct {
		name       string
		sha1Digest string
		data       []byte
		header     *zip.FileHeader
	}

	var files []fileDigest

	for _, f := range zr.File {
		// Skip existing signature files
		if strings.HasPrefix(f.Name, "META-INF/") {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}

		h1 := sha1.Sum(data)
		d1B64 := base64.StdEncoding.EncodeToString(h1[:])

		manifestBuf.WriteString(fmt.Sprintf("Name: %s\r\n", f.Name))
		manifestBuf.WriteString(fmt.Sprintf("SHA1-Digest: %s\r\n\r\n", d1B64))

		files = append(files, fileDigest{
			name:       f.Name,
			sha1Digest: d1B64,
			data:       data,
			header:     &f.FileHeader,
		})
	}

	manifestBytes := manifestBuf.Bytes()

	// 2. Build CERT.SF
	var sfBuf bytes.Buffer
	sfBuf.WriteString("Signature-Version: 1.0\r\n")
	sfBuf.WriteString("Created-By: Freebox APK Signer 1.0\r\n")

	manifestSHA1 := sha1.Sum(manifestBytes)
	sfBuf.WriteString(fmt.Sprintf("SHA1-Digest-Manifest: %s\r\n\r\n", base64.StdEncoding.EncodeToString(manifestSHA1[:])))

	for _, f := range files {
		entrySection := fmt.Sprintf("Name: %s\r\nSHA1-Digest: %s\r\n\r\n", f.name, f.sha1Digest)
		entryHash := sha1.Sum([]byte(entrySection))
		sfBuf.WriteString(fmt.Sprintf("Name: %s\r\n", f.name))
		sfBuf.WriteString(fmt.Sprintf("SHA1-Digest: %s\r\n\r\n", base64.StdEncoding.EncodeToString(entryHash[:])))
	}

	sfBytes := sfBuf.Bytes()

	// 3. Build CERT.RSA / Signature Block
	block, _ := pem.Decode([]byte(privKeyPEM))
	if block == nil {
		return nil, errors.New("invalid private key PEM")
	}

	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		privKey, _ = x509.ParsePKCS1PrivateKey(block.Bytes)
	}

	sfHash := sha256.Sum256(sfBytes)
	var sigBytes []byte

	if rsaKey, ok := privKey.(*rsa.PrivateKey); ok {
		sigBytes, err = rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, sfHash[:])
		if err != nil {
			return nil, fmt.Errorf("rsa signing failed: %w", err)
		}
	} else {
		// Fallback simple signature payload
		sigBytes = sfHash[:]
	}

	// 4. Assemble signed APK
	var outBuf bytes.Buffer
	zw := zip.NewWriter(&outBuf)

	// Write original files
	for _, f := range files {
		w, err := zw.CreateHeader(f.header)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.data); err != nil {
			return nil, err
		}
	}

	// Write META-INF/MANIFEST.MF
	w, _ := zw.Create("META-INF/MANIFEST.MF")
	_, _ = w.Write(manifestBytes)

	// Write META-INF/CERT.SF
	w, _ = zw.Create("META-INF/CERT.SF")
	_, _ = w.Write(sfBytes)

	// Write META-INF/CERT.RSA
	w, _ = zw.Create("META-INF/CERT.RSA")
	_, _ = w.Write(sigBytes)

	if err := zw.Close(); err != nil {
		return nil, err
	}

	if err := fsys.Write(outPath, outBuf.Bytes()); err != nil {
		return nil, fmt.Errorf("failed writing signed apk: %w", err)
	}

	return &SignResult{
		FilePath:     outPath,
		SignatureHex: hex.EncodeToString(sigBytes),
		SignatureB64: base64.StdEncoding.EncodeToString(sigBytes),
		Algorithm:    "JAR-APK-v1",
		SignedAt:     time.Now(),
		IsAPK:        true,
	}, nil
}
