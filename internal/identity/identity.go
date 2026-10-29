package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)

type Identity struct {
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

type keyFile struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// Generate creates a fresh Ed25519 machine identity.
func Generate() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}
	return &Identity{Public: pub, Private: priv}, nil
}

// Save writes the identity to path with owner-only permissions.
func (i *Identity) Save(path string) error {
	kf := keyFile{
		Public:  base64.StdEncoding.EncodeToString(i.Public),
		Private: base64.StdEncoding.EncodeToString(i.Private),
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
	pub, err := base64.StdEncoding.DecodeString(kf.Public)
	if err != nil {
		return nil, fmt.Errorf("failed to decode public key: %w", err)
	}
	priv, err := base64.StdEncoding.DecodeString(kf.Private)
	if err != nil {
		return nil, fmt.Errorf("failed to decode private key: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize || len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("key file %s has wrong key sizes", path)
	}
	return &Identity{Public: ed25519.PublicKey(pub), Private: ed25519.PrivateKey(priv)}, nil
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

// Fingerprint returns the short public key fingerprint of pub.
func Fingerprint(pub []byte) string {
	return hex.EncodeToString(pub[:4])
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
