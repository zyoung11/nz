package identity

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

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

type keyFile struct {
	SignPublic  string `json:"sign_public"`
	SignPrivate string `json:"sign_private"`
	ExchPublic  string `json:"exch_public"`
	ExchPrivate string `json:"exch_private"`
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

// Save writes the identity to path with owner-only permissions.
func (i *Identity) Save(path string) error {
	kf := keyFile{
		SignPublic:  base64.StdEncoding.EncodeToString(i.SignPublic),
		SignPrivate: base64.StdEncoding.EncodeToString(i.SignPrivate),
		ExchPublic:  base64.StdEncoding.EncodeToString(i.ExchPublic),
		ExchPrivate: base64.StdEncoding.EncodeToString(i.ExchPrivate.Bytes()),
	}
	data, err := json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode key file: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write key file %s: %w", path, err)
	}
	return nil
}

// Load reads an identity from path.
func Load(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file %s: %w", path, err)
	}
	var kf keyFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return nil, fmt.Errorf("failed to parse key file %s: %w", path, err)
	}
	signPub, err := base64.StdEncoding.DecodeString(kf.SignPublic)
	if err != nil || len(signPub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("key file %s has a bad signing public key", path)
	}
	signPriv, err := base64.StdEncoding.DecodeString(kf.SignPrivate)
	if err != nil || len(signPriv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("key file %s has a bad signing private key", path)
	}
	exchPub, err := base64.StdEncoding.DecodeString(kf.ExchPublic)
	if err != nil || len(exchPub) != ExchangeKeySize {
		return nil, fmt.Errorf("key file %s has a bad exchange public key", path)
	}
	exchPrivBytes, err := base64.StdEncoding.DecodeString(kf.ExchPrivate)
	if err != nil {
		return nil, fmt.Errorf("key file %s has a bad exchange private key", path)
	}
	exchPriv, err := ecdh.X25519().NewPrivateKey(exchPrivBytes)
	if err != nil {
		return nil, fmt.Errorf("key file %s has a bad exchange private key: %w", path, err)
	}
	return &Identity{
		SignPublic:  ed25519.PublicKey(signPub),
		SignPrivate: ed25519.PrivateKey(signPriv),
		ExchPublic:  exchPub,
		ExchPrivate: exchPriv,
	}, nil
}

// LoadOrCreate returns the identity at path, generating one on first use.
func LoadOrCreate(path string) (*Identity, error) {
	if _, err := os.Stat(path); err == nil {
		return Load(path)
	}
	id, err := Generate()
	if err != nil {
		return nil, err
	}
	if err := id.Save(path); err != nil {
		return nil, err
	}
	return id, nil
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
