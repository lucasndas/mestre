package app

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
)

// writeGuildCA keeps the CA certificate of the Guild, in PEM, at path: it is
// public, and `mestre attach` (a command of people, which has no database) checks
// the panel by it. The file is replaced whole, and left alone when it is already
// the same.
func writeGuildCA(path string, ca *x509.Certificate) error {
	if ca == nil {
		return nil
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ca-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
