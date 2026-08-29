package ipc

import (
	"encoding/json"

	"freebox/signer"
)

func init() {
	register("crypto.signFile", handleCryptoSignFile)
	register("crypto.verifyFileSignature", handleCryptoVerifyFileSignature)
}

type cryptoSignFileParams struct {
	Path       string `json:"path"`
	PrivKeyPEM string `json:"privKeyPem"`
}

func handleCryptoSignFile(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p cryptoSignFileParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}
	sigRes, err := signer.SignDetached(fs, subPath, p.PrivKeyPEM)
	if err != nil {
		return nil, err
	}
	return map[string]string{"signatureHex": sigRes.SignatureHex}, nil
}

type cryptoVerifyParams struct {
	Path      string `json:"path"`
	SigHex    string `json:"sigHex"`
	PubKeyPEM string `json:"pubKeyPem"`
}

func handleCryptoVerifyFileSignature(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p cryptoVerifyParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	mountName, subPath := resolveMountPath(p.Path)
	fs, ok := inst.Mounts.Get(mountName)
	if !ok {
		return nil, errMountNotFound(mountName)
	}
	valid, err := signer.VerifyDetached(fs, subPath, p.SigHex, p.PubKeyPEM)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"valid": valid}, nil
}
