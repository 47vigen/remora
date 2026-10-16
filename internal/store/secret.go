package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoSealer means a repository needed to seal, open or MAC a secret but the store has no Sealer.
var ErrNoSealer = errors.New("store: no sealer configured")

const (
	masterKeyLen  = 32
	masterKeyFile = "master.key"
	secretsInfo   = "remora/secrets/v1" //nolint:gosec // an HKDF info label, not a credential
	keyhashInfo   = "remora/keyhash/v1"
)

// Sealer encrypts secrets at rest with AES-256-GCM and computes lookup MACs with HMAC-SHA256. Both keys are
// derived from the master key with HKDF-SHA256, so the master key itself is never used directly.
type Sealer struct {
	aead cipher.AEAD
	mac  []byte
}

// NewSealer derives the sealing and MAC keys from a 32-byte master key.
func NewSealer(master []byte) (*Sealer, error) {
	if len(master) != masterKeyLen {
		return nil, fmt.Errorf("store: master key is %d bytes, want %d", len(master), masterKeyLen)
	}
	encKey, err := hkdf.Key(sha256.New, master, nil, secretsInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("store: derive secrets key: %w", err)
	}
	macKey, err := hkdf.Key(sha256.New, master, nil, keyhashInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("store: derive key-hash key: %w", err)
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, fmt.Errorf("store: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("store: gcm: %w", err)
	}
	return &Sealer{aead: aead, mac: macKey}, nil
}

// Seal encrypts plaintext bound to aad (the row it belongs to, e.g. "provider_key:<id>"). The random nonce is
// prepended to the ciphertext.
func (s *Sealer) Seal(plaintext, aad string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize(), s.aead.NonceSize()+len(plaintext)+s.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("store: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(aad)), nil
}

// Open decrypts a value made by Seal with the same aad.
func (s *Sealer) Open(sealed []byte, aad string) (string, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n+s.aead.Overhead() {
		return "", errors.New("store: open secret: value too short")
	}
	pt, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(aad))
	if err != nil {
		return "", fmt.Errorf("store: open secret %s: %w", aad, err)
	}
	return string(pt), nil
}

// MAC returns HMAC-SHA256(macKey, data), used to look up client keys without storing them.
func (s *Sealer) MAC(data []byte) []byte {
	h := hmac.New(sha256.New, s.mac)
	h.Write(data)
	return h.Sum(nil)
}

// SetSealer sets the Sealer the repositories use for secrets.
func (s *Store) SetSealer(sl *Sealer) {
	s.sealer.Store(sl)
	_ = s.Refresh(context.Background()) // a failure leaves reads on SQL, which reports it
}

// LoadMasterKey returns the master key. env (the value of REMORA_MASTER_KEY, base64 of 32 bytes) wins when set;
// otherwise the key is read from dataDir/master.key, which is created (mode 0600) with a random key if absent.
func LoadMasterKey(dataDir, env string) ([]byte, error) {
	if env = strings.TrimSpace(env); env != "" {
		key, err := base64.StdEncoding.DecodeString(env)
		if err != nil {
			key, err = base64.RawStdEncoding.DecodeString(env)
		}
		if err != nil {
			return nil, fmt.Errorf("store: REMORA_MASTER_KEY is not base64: %w", err)
		}
		if len(key) != masterKeyLen {
			return nil, fmt.Errorf("store: REMORA_MASTER_KEY decodes to %d bytes, want %d", len(key), masterKeyLen)
		}
		return key, nil
	}
	p := filepath.Join(dataDir, masterKeyFile)
	key, err := readMasterKey(p)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}
	if err := createMasterKey(dataDir, p); err != nil {
		return nil, err
	}
	return readMasterKey(p)
}

func readMasterKey(p string) ([]byte, error) {
	key, err := os.ReadFile(p) //nolint:gosec // path is the data directory's master.key
	if err != nil {
		return nil, fmt.Errorf("store: read master key: %w", err)
	}
	if len(key) != masterKeyLen {
		return nil, fmt.Errorf("store: %s is %d bytes, want %d", p, len(key), masterKeyLen)
	}
	return key, nil
}

// createMasterKey writes a random key to a temporary file and links it into place, so a concurrent process
// never reads a partial file and the first writer wins.
func createMasterKey(dataDir, p string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("store: create data dir: %w", err)
	}
	key := make([]byte, masterKeyLen)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("store: generate master key: %w", err)
	}
	tmp, err := os.CreateTemp(dataDir, ".master.key-*")
	if err != nil {
		return fmt.Errorf("store: create master key: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("store: write master key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("store: sync master key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close master key: %w", err)
	}
	if err := os.Link(tmp.Name(), p); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("store: install master key: %w", err)
	}
	return nil
}
