package identity

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)

const (
	PublicBlobSize  = ed25519.PublicKeySize + 32
	SignatureSize   = ed25519.SignatureSize
	ExchangeKeySize = 32
)

type Identity struct {
	SignPublic  ed25519.PublicKey
	SignPrivate ed25519.PrivateKey
	ExchPublic  []byte
	ExchPrivate *ecdh.PrivateKey
}

// Generate creates a fresh machine identity with a signing key
// and a key exchange key.
func Generate() (*Identity, error) {
	signPub, signPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signing key: %w", err)
	}
	exchPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate exchange key: %w", err)
	}
	return &Identity{
		SignPublic:  signPub,
		SignPrivate: signPriv,
		ExchPublic:  exchPriv.PublicKey().Bytes(),
		ExchPrivate: exchPriv,
	}, nil
}

// Parts returns the identity as base64 fields for storage.
func (i *Identity) Parts() (string, string, string, string) {
	return base64.StdEncoding.EncodeToString(i.SignPublic),
		base64.StdEncoding.EncodeToString(i.SignPrivate),
		base64.StdEncoding.EncodeToString(i.ExchPublic),
		base64.StdEncoding.EncodeToString(i.ExchPrivate.Bytes())
}

// FromParts rebuilds an identity from stored base64 fields.
func FromParts(signPub, signPriv, exchPub, exchPriv string) (*Identity, error) {
	pub, err := base64.StdEncoding.DecodeString(signPub)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("bad signing public key")
	}
	priv, err := base64.StdEncoding.DecodeString(signPriv)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("bad signing private key")
	}
	exPub, err := base64.StdEncoding.DecodeString(exchPub)
	if err != nil || len(exPub) != ExchangeKeySize {
		return nil, fmt.Errorf("bad exchange public key")
	}
	exPrivBytes, err := base64.StdEncoding.DecodeString(exchPriv)
	if err != nil {
		return nil, fmt.Errorf("bad exchange private key")
	}
	exPriv, err := ecdh.X25519().NewPrivateKey(exPrivBytes)
	if err != nil {
		return nil, fmt.Errorf("bad exchange private key: %w", err)
	}
	return &Identity{
		SignPublic:  ed25519.PublicKey(pub),
		SignPrivate: ed25519.PrivateKey(priv),
		ExchPublic:  exPub,
		ExchPrivate: exPriv,
	}, nil
}

// Sign signs msg with the machine signing key.
func (i *Identity) Sign(msg []byte) []byte {
	return ed25519.Sign(i.SignPrivate, msg)
}

// Verify checks sig over msg against a signing public key.
func Verify(signPub, msg, sig []byte) bool {
	if len(signPub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(signPub), msg, sig)
}

// SharedKey derives the session key for a peer with ECDH and HKDF.
func (i *Identity) SharedKey(peerExchPub []byte) ([]byte, error) {
	peer, err := ecdh.X25519().NewPublicKey(peerExchPub)
	if err != nil {
		return nil, fmt.Errorf("bad peer exchange key: %w", err)
	}
	secret, err := i.ExchPrivate.ECDH(peer)
	if err != nil {
		return nil, fmt.Errorf("key exchange failed: %w", err)
	}
	reader := hkdf.New(sha256.New, secret, nil, []byte("n0-session-v1"))
	key := make([]byte, 32)
	if _, err := reader.Read(key); err != nil {
		return nil, fmt.Errorf("session key derivation failed: %w", err)
	}
	return key, nil
}

// PublicBlob packs the signing and exchange public keys.
func (i *Identity) PublicBlob() []byte {
	blob := make([]byte, 0, PublicBlobSize)
	blob = append(blob, i.SignPublic...)
	blob = append(blob, i.ExchPublic...)
	return blob
}

// SplitPublic unpacks a public blob into signing and exchange keys.
func SplitPublic(blob []byte) ([]byte, []byte, error) {
	if len(blob) != PublicBlobSize {
		return nil, nil, fmt.Errorf("public blob is %d bytes, want %d", len(blob), PublicBlobSize)
	}
	return blob[:ed25519.PublicKeySize], blob[ed25519.PublicKeySize:], nil
}

// Fingerprint returns the short public key fingerprint of blob.
func Fingerprint(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:4])
}

// UserSalt derives the fixed salt for a user, so every machine of the
// same user derives the same verifier from the same password.
func UserSalt(user string) []byte {
	return []byte("n0-salt-v1|" + user)
}

// NewSalt returns a random salt for verifier derivation.
func NewSalt() ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}
	return salt, nil
}

// Verifier derives the password verifier with Argon2id.
func Verifier(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
}
