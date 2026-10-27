package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func TestVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sums := []byte("abc  inlinr-linux-amd64\n")
	sig := ed25519.Sign(priv, sums)

	if err := verify(pub, sums, sig); err != nil {
		t.Fatalf("a good signature was refused: %v", err)
	}
	tampered := []byte("abd  inlinr-linux-amd64\n")
	if err := verify(pub, tampered, sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a changed list was accepted: %v", err)
	}
	if err := verify(pub, sums, sig[:10]); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a truncated signature was accepted: %v", err)
	}
}

func TestEmbeddedKeyParses(t *testing.T) {
	if _, err := ParsePublicKey(publicKeyPEM); err != nil {
		t.Fatalf("release-key.pub.pem: %v", err)
	}
}
