package identity

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n0.key")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("load or create: %v", err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !bytes.Equal(first.SignPublic, second.SignPublic) {
		t.Errorf("signing public key changed after reload")
	}
	if !bytes.Equal(first.ExchPublic, second.ExchPublic) {
		t.Errorf("exchange public key changed after reload")
	}
	if Fingerprint(first.PublicBlob()) != Fingerprint(second.PublicBlob()) {
		t.Errorf("fingerprint changed after reload")
	}
}

func TestSignAndVerify(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	msg := []byte("hello")
	sig := id.Sign(msg)
	if !Verify(id.SignPublic, msg, sig) {
		t.Errorf("valid signature rejected")
	}
	if Verify(id.SignPublic, []byte("other"), sig) {
		t.Errorf("bad signature accepted")
	}
}

func TestSharedKeyAgreement(t *testing.T) {
	a, _ := Generate()
	b, _ := Generate()
	ka, err := a.SharedKey(b.ExchPublic)
	if err != nil {
		t.Fatalf("shared key a: %v", err)
	}
	kb, err := b.SharedKey(a.ExchPublic)
	if err != nil {
		t.Fatalf("shared key b: %v", err)
	}
	if !bytes.Equal(ka, kb) {
		t.Errorf("shared keys differ")
	}
}

func TestPublicBlobRoundTrip(t *testing.T) {
	id, _ := Generate()
	signPub, exchPub, err := SplitPublic(id.PublicBlob())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if !bytes.Equal(signPub, id.SignPublic) || !bytes.Equal(exchPub, id.ExchPublic) {
		t.Errorf("blob round trip mismatch")
	}
	if _, _, err := SplitPublic([]byte("short")); err == nil {
		t.Errorf("short blob accepted")
	}
}

func TestVerifierIsDeterministic(t *testing.T) {
	salt, err := NewSalt()
	if err != nil {
		t.Fatalf("salt: %v", err)
	}
	a := Verifier("pw", salt)
	b := Verifier("pw", salt)
	if !bytes.Equal(a, b) {
		t.Errorf("verifier not deterministic")
	}
	c := Verifier("other", salt)
	if bytes.Equal(a, c) {
		t.Errorf("different passwords gave same verifier")
	}
}

func TestFingerprintLength(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := len(Fingerprint(id.PublicBlob())); got != 8 {
		t.Errorf("fingerprint length = %d, want 8", got)
	}
}
