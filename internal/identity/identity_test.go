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
	if !bytes.Equal(first.Public, second.Public) {
		t.Errorf("public key changed after reload")
	}
	if Fingerprint(first.Public) != Fingerprint(second.Public) {
		t.Errorf("fingerprint changed after reload")
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
	if got := len(Fingerprint(id.Public)); got != 8 {
		t.Errorf("fingerprint length = %d, want 8", got)
	}
}
