package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/netip"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	nonceSize  = chacha20poly1305.NonceSizeX
	signOffset = 1 + 4 + 4
)

// SignedMessage is the authenticated envelope for data and control
// messages between machines. The signature covers type, addresses
// and payload, so the routing header cannot be forged.
type SignedMessage struct {
	Type    MsgType
	SrcIP   netip.Addr
	DstIP   netip.Addr
	Payload []byte
}

func addr4(a netip.Addr) [4]byte {
	if a.Is4() {
		return a.As4()
	}
	return [4]byte{}
}

// SignBytes returns the byte range covered by the signature.
func (m SignedMessage) signBytes() []byte {
	src := addr4(m.SrcIP)
	dst := addr4(m.DstIP)
	out := make([]byte, 0, signOffset+len(m.Payload))
	out = append(out, byte(m.Type))
	out = append(out, src[:]...)
	out = append(out, dst[:]...)
	out = append(out, m.Payload...)
	return out
}

// MarshalSigned encodes and signs the message.
func MarshalSigned(m SignedMessage, sign func([]byte) []byte) []byte {
	signed := m.signBytes()
	sig := sign(signed)
	out := make([]byte, 0, len(signed)+ed25519.SignatureSize)
	out = append(out, signed...)
	out = append(out, sig...)
	return out
}

// ParseSigned decodes a signed message and its signature.
func ParseSigned(data []byte) (SignedMessage, []byte, error) {
	if len(data) < signOffset+ed25519.SignatureSize {
		return SignedMessage{}, nil, fmt.Errorf("signed message too short: %d bytes", len(data))
	}
	sig := data[len(data)-ed25519.SignatureSize:]
	signed := data[:len(data)-ed25519.SignatureSize]
	src := netip.AddrFrom4([4]byte{signed[1], signed[2], signed[3], signed[4]})
	dst := netip.AddrFrom4([4]byte{signed[5], signed[6], signed[7], signed[8]})
	m := SignedMessage{
		Type:    MsgType(signed[0]),
		SrcIP:   src,
		DstIP:   dst,
		Payload: signed[signOffset:],
	}
	return m, sig, nil
}

// VerifySigned checks the signature of a parsed message against a
// signing public key.
func VerifySigned(m SignedMessage, sig, signPub []byte) bool {
	if len(signPub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(signPub), m.signBytes(), sig)
}

// Seal encrypts plaintext with XChaCha20-Poly1305, returning nonce
// followed by ciphertext.
func Seal(key, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("bad session key: %w", err)
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a blob produced by Seal.
func Open(key, blob []byte) ([]byte, error) {
	if len(blob) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("bad session key: %w", err)
	}
	return aead.Open(nil, blob[:nonceSize], blob[nonceSize:], nil)
}

// PeerInfo describes a machine for peer discovery.
type PeerInfo struct {
	User        string `json:"user"`
	Machine     string `json:"machine"`
	VPNIP       string `json:"vpn_ip"`
	SignPub     []byte `json:"sign_pub"`
	ExchPub     []byte `json:"exch_pub"`
	Trusted     bool   `json:"trusted"`
	Status      string `json:"status"`
	Self        bool   `json:"self"`
	CanCall     bool   `json:"can_call"`
	CanCallBack bool   `json:"can_call_back"`
}

// PeerIntro tells a machine that another machine wants to reach it,
// so both sides can punch through NAT at the same time.
type PeerIntro struct {
	VPNIP string         `json:"vpn_ip"`
	Addr  netip.AddrPort `json:"addr"`
}

// AdminRequest is a management operation sent from an admin machine.
type AdminRequest struct {
	Command string `json:"command"`
	Target  string `json:"target,omitempty"`
	To      string `json:"to,omitempty"`
}

// AdminReply carries the management result.
type AdminReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  string `json:"data,omitempty"`
}

// MachineListRpy carries the machine table visible to the requester.
type MachineListRpy struct {
	Peers []PeerInfo `json:"peers"`
}

// PeerQueryPayload encodes the discovery target address.
func PeerQueryPayload(target netip.Addr) []byte {
	return target.AsSlice()
}

// ParsePeerQuery decodes a discovery target address.
func ParsePeerQuery(payload []byte) (netip.Addr, error) {
	if len(payload) != 4 {
		return netip.Addr{}, fmt.Errorf("peer query payload is %d bytes, want 4", len(payload))
	}
	addr, ok := netip.AddrFromSlice(payload)
	if !ok {
		return netip.Addr{}, fmt.Errorf("bad peer query address")
	}
	return addr, nil
}

// PeerAnswer carries the keys of a machine and, when policy allows
// direct connections, its real address. CanCall reports whether the
// requester may start new calls to the machine. CanCallBack reports
// whether the machine may start new calls back.
type PeerAnswer struct {
	Found       bool
	HasAddr     bool
	CanCall     bool
	CanCallBack bool
	Addr        netip.AddrPort
	SignPub     []byte
	ExchPub     []byte
}

// MarshalPeerAnswer encodes a peer answer payload.
func MarshalPeerAnswer(a PeerAnswer) []byte {
	if !a.Found {
		return []byte{0}
	}
	out := make([]byte, 0, 4+4+2+PublicBlobSize)
	out = append(out, 1)
	out = append(out, boolByte(a.HasAddr), boolByte(a.CanCall), boolByte(a.CanCallBack))
	if a.HasAddr {
		addr := addr4(a.Addr.Addr())
		out = append(out, addr[:]...)
		port := a.Addr.Port()
		out = append(out, byte(port>>8), byte(port))
	}
	out = append(out, a.SignPub...)
	out = append(out, a.ExchPub...)
	return out
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// ParsePeerAnswer decodes a peer answer payload.
func ParsePeerAnswer(payload []byte) (PeerAnswer, error) {
	if len(payload) < 1 {
		return PeerAnswer{}, fmt.Errorf("peer answer too short")
	}
	if payload[0] == 0 {
		return PeerAnswer{Found: false}, nil
	}
	if len(payload) < 4 {
		return PeerAnswer{}, fmt.Errorf("peer answer too short")
	}
	a := PeerAnswer{
		Found:       true,
		HasAddr:     payload[1] == 1,
		CanCall:     payload[2] == 1,
		CanCallBack: payload[3] == 1,
	}
	offset := 4
	if a.HasAddr {
		if len(payload) < 4+4+2+PublicBlobSize {
			return PeerAnswer{}, fmt.Errorf("peer answer is %d bytes", len(payload))
		}
		addr, ok := netip.AddrFromSlice(payload[4:8])
		if !ok {
			return PeerAnswer{}, fmt.Errorf("bad peer answer address")
		}
		port := uint16(payload[8])<<8 | uint16(payload[9])
		a.Addr = netip.AddrPortFrom(addr, port)
		offset = 10
	}
	if len(payload) != offset+PublicBlobSize {
		return PeerAnswer{}, fmt.Errorf("peer answer is %d bytes", len(payload))
	}
	a.SignPub = payload[offset : offset+ed25519.PublicKeySize]
	a.ExchPub = payload[offset+ed25519.PublicKeySize:]
	return a, nil
}

const PublicBlobSize = ed25519.PublicKeySize + 32
