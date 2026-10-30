package protocol

import (
	"net/netip"
	"testing"

	"nz/internal/identity"
)

func TestSignedRoundTrip(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	m := SignedMessage{
		Type:    TypeData,
		SrcIP:   netip.MustParseAddr("192.168.200.3"),
		DstIP:   netip.MustParseAddr("192.168.200.4"),
		Payload: []byte("payload-bytes"),
	}
	data := MarshalSigned(m, id.Sign)
	parsed, sig, err := ParseSigned(data)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySigned(parsed, sig, id.SignPublic) {
		t.Fatal("verify failed on round trip")
	}
	signPub, exchPub, err := identity.SplitPublic(id.PublicBlob())
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySigned(parsed, sig, signPub) {
		t.Fatal("verify failed with split public blob")
	}
	other, _ := identity.Generate()
	k1, err := other.SharedKey(exchPub)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := id.SharedKey(other.ExchPublic)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := Seal(k1, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Open(k2, cipher)
	if err != nil || string(plain) != "secret" {
		t.Fatalf("seal open mismatch: %v %q", err, plain)
	}
}
