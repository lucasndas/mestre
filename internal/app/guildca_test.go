package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The engine keeps the CA of the Guild, in PEM, in the configuration folder, for
// `mestre attach` to check the panel by; writing it again changes nothing.
func TestTheGuildCAIsKeptInPEMAndWritingItAgainChangesNothing(t *testing.T) {
	t.Parallel()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: time.Unix(0, 0), NotAfter: time.Unix(1<<31, 0), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	path := filepath.Join(t.TempDir(), GuildCAFile)
	if err := writeGuildCA(path, ca); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if blk, _ := pem.Decode(data); blk == nil || blk.Type != "CERTIFICATE" || string(blk.Bytes) != string(der) {
		t.Fatalf("not the PEM of the CA: %q", data)
	}
	before, _ := os.Stat(path)
	if err := writeGuildCA(path, ca); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the file was written again")
	}
	if err := writeGuildCA(path, nil); err != nil {
		t.Fatal(err)
	}
}
