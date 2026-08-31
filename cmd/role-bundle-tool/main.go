package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tekroo-ai/teams/organization"
)

func main() {
	mode := flag.String("mode", "", "keygen, sign, or digest")
	privatePath := flag.String("private-key", "", "private key file")
	publicPath := flag.String("public-key", "", "public key file")
	bundlePath := flag.String("bundle", "", "role bundle file")
	flag.Parse()
	var err error
	switch *mode {
	case "keygen":
		err = keygen(*privatePath, *publicPath)
	case "sign":
		err = sign(*privatePath, *bundlePath)
	case "digest":
		err = digest(*bundlePath)
	default:
		err = errors.New("mode must be keygen, sign, or digest")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func digest(bundlePath string) error {
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	var bundle organization.RoleBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return err
	}
	value, err := bundle.ContentDigest()
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func keygen(privatePath, publicPath string) error {
	if privatePath == "" || publicPath == "" || privatePath == publicPath {
		return errors.New("distinct private and public key paths are required")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := writeAtomic(privatePath, []byte(base64.StdEncoding.EncodeToString(privateKey)+"\n"), 0o600); err != nil {
		return err
	}
	return writeAtomic(publicPath, []byte(base64.StdEncoding.EncodeToString(publicKey)+"\n"), 0o644)
}

func sign(privatePath, bundlePath string) error {
	privateRaw, err := os.ReadFile(privatePath)
	if err != nil {
		return err
	}
	privateKey, err := base64.StdEncoding.Strict().DecodeString(string(trimSpace(privateRaw)))
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("private key is not a base64 Ed25519 private key")
	}
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	var bundle organization.RoleBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return err
	}
	digest, err := bundle.ContentDigest()
	if err != nil {
		return err
	}
	digestBytes, err := hex.DecodeString(string(digest))
	if err != nil {
		return err
	}
	bundle.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(privateKey), digestBytes))
	formatted, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(bundlePath, append(formatted, '\n'), 0o644)
}

func writeAtomic(path string, raw []byte, mode os.FileMode) error {
	if path == "" {
		return errors.New("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".role-bundle-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func trimSpace(raw []byte) []byte {
	start := 0
	for start < len(raw) && (raw[start] == ' ' || raw[start] == '\n' || raw[start] == '\r' || raw[start] == '\t') {
		start++
	}
	end := len(raw)
	for end > start && (raw[end-1] == ' ' || raw[end-1] == '\n' || raw[end-1] == '\r' || raw[end-1] == '\t') {
		end--
	}
	return raw[start:end]
}
