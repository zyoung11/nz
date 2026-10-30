package protocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

const Version byte = 0x03

type MsgType byte

const (
	TypeJoinRequest    MsgType = 0x01
	TypeJoinChallenge  MsgType = 0x02
	TypeJoinProof      MsgType = 0x03
	TypeJoinReply      MsgType = 0x04
	TypeApproveNotify  MsgType = 0x05
	TypeKeepAlive      MsgType = 0x10
	TypeData           MsgType = 0x11
	TypeRelayData      MsgType = 0x12
	TypePeerQuery      MsgType = 0x20
	TypePeerQueryRpy   MsgType = 0x21
	TypePeerIntro      MsgType = 0x22
	TypeMachineList    MsgType = 0x30
	TypeMachineListRpy MsgType = 0x31
	TypeAdminRequest   MsgType = 0x32
	TypeAdminRpy       MsgType = 0x33
	TypePolicyChanged  MsgType = 0x34
	TypeDisconnect     MsgType = 0x3f
)

const headerLen = 6

type Envelope struct {
	Type MsgType
	Body []byte
}

// Marshal encodes the envelope as version, type, length and body.
func Marshal(env Envelope) []byte {
	out := make([]byte, headerLen+len(env.Body))
	out[0] = Version
	out[1] = byte(env.Type)
	binary.BigEndian.PutUint32(out[2:6], uint32(len(env.Body)))
	copy(out[6:], env.Body)
	return out
}

// Unmarshal decodes one envelope from data.
func Unmarshal(data []byte) (Envelope, error) {
	if len(data) < headerLen {
		return Envelope{}, fmt.Errorf("message too short: %d bytes", len(data))
	}
	if data[0] != Version {
		return Envelope{}, fmt.Errorf("protocol version %02x, want %02x", data[0], Version)
	}
	bodyLen := binary.BigEndian.Uint32(data[2:6])
	if uint32(len(data)-headerLen) != bodyLen {
		return Envelope{}, fmt.Errorf("body length %d, want %d", len(data)-headerLen, bodyLen)
	}
	body := make([]byte, bodyLen)
	copy(body, data[6:])
	return Envelope{Type: MsgType(data[1]), Body: body}, nil
}

type JoinRequest struct {
	User     string `json:"user"`
	Machine  string `json:"machine"`
	Public   []byte `json:"public"`
	Salt     []byte `json:"salt,omitempty"`
	Verifier []byte `json:"verifier,omitempty"`
}

type JoinChallenge struct {
	Nonce []byte `json:"nonce"`
}

type JoinProof struct {
	User    string `json:"user"`
	Machine string `json:"machine"`
	Public  []byte `json:"public"`
	Proof   []byte `json:"proof"`
}

type JoinReply struct {
	Result    string `json:"result"`
	Error     string `json:"error,omitempty"`
	VPNIP     string `json:"vpn_ip,omitempty"`
	ServerPub []byte `json:"server_pub,omitempty"`
}

type ApproveNotify struct {
	VPNIP string `json:"vpn_ip"`
}

const (
	ResultActive   = "active"
	ResultPending  = "pending"
	ResultRejected = "rejected"
)

// EncodeJSON encodes a control message body.
func EncodeJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// DecodeJSON decodes a control message body.
func DecodeJSON(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("failed to decode message body: %w", err)
	}
	return nil
}

// Proof computes the join proof bound to user, machine, public key and nonce.
func Proof(verifier []byte, nonce []byte, user, machine string, public []byte) []byte {
	mac := hmac.New(sha256.New, verifier)
	mac.Write(nonce)
	mac.Write([]byte(user))
	mac.Write([]byte{0})
	mac.Write([]byte(machine))
	mac.Write([]byte{0})
	mac.Write(public)
	return mac.Sum(nil)
}

// VerifyProof reports whether proof matches the expected verifier.
func VerifyProof(verifier, proof, nonce []byte, user, machine string, public []byte) bool {
	return hmac.Equal(proof, Proof(verifier, nonce, user, machine, public))
}
