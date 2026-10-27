// Package release checks that a release's SHA256SUMS.txt was signed with
// Inlinr's release key before anything in it is trusted.
//
// SHA256SUMS.txt alone only proves a binary matches a list published next
// to it: whoever can change one can change the other. The list is signed in
// release.yml with a private key held as a repository secret, and checked
// here against the public key built into the binary.
package release

import (
	"crypto/ed25519"
	"crypto/x509"
	_ "embed"
	"encoding/pem"
	"errors"
)

// The public half of the key release.yml signs with. Rotating it means a CLI
// carrying the new key must reach users before a release is signed with it.
//
//go:embed release-key.pub.pem
var publicKeyPEM []byte

// ErrBadSignature is a SHA256SUMS.txt that the release key did not sign.
var ErrBadSignature = errors.New("SHA256SUMS.txt signature does not match the release key")

// VerifySums checks sig (SHA256SUMS.txt.sig, raw Ed25519) over sums.
func VerifySums(sums, sig []byte) error {
	key, err := ParsePublicKey(publicKeyPEM)
	if err != nil {
		return err
	}
	return verify(key, sums, sig)
}

func verify(key ed25519.PublicKey, sums, sig []byte) error {
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, sums, sig) {
		return ErrBadSignature
	}
	return nil
}

// ParsePublicKey reads an Ed25519 public key in PEM (PKIX), as
// `openssl pkey -pubout` writes it.
func ParsePublicKey(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("release key: no PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("release key: not an Ed25519 key")
	}
	return key, nil
}
