// Package omniroute reads an OmniRoute data directory read-only and converts it into a remora.yaml document,
// its secrets and the OAuth credential files remora's accounts import.
package omniroute

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// Field encryption as OmniRoute does it (src/lib/db/encryption.ts at 9ecddd15): "enc:v1:<iv_hex>:<ct_hex>:<tag_hex>",
// AES-256-GCM with a 16-byte IV and a 16-byte tag, and a key derived with scrypt from STORAGE_ENCRYPTION_KEY.
const (
	prefix     = "enc:v1:"
	staticSalt = "omniroute-field-encryption-v1"
	secretVar  = "STORAGE_ENCRYPTION_KEY"
)

// ErrUndecryptable is returned for a well-formed value that no derived key opens.
var ErrUndecryptable = errors.New("value does not decrypt with this STORAGE_ENCRYPTION_KEY")

// Decrypter opens OmniRoute's encrypted fields.
type Decrypter struct{ keys [][]byte }

// NewDecrypter derives both keys OmniRoute may have used: the current one (static salt) and the legacy one (salt =
// the first 16 bytes of sha256(secret)). scrypt parameters are Node's scryptSync defaults: N=16384, r=8, p=1.
func NewDecrypter(secret string) *Decrypter {
	sum := sha256.Sum256([]byte(secret))
	d := &Decrypter{}
	for _, salt := range [][]byte{[]byte(staticSalt), sum[:16]} {
		k, err := scrypt.Key([]byte(secret), salt, 16384, 8, 1, 32)
		if err != nil { // only for invalid parameters, which are constants here
			panic(err)
		}
		d.keys = append(d.keys, k)
	}
	return d
}

// Decrypt returns a value without the "enc:v1:" prefix unchanged, and otherwise the plaintext. A malformed value is
// an error; a well-formed one that neither key opens is ErrUndecryptable.
func (d *Decrypter) Decrypt(v string) (string, error) {
	body, ok := strings.CutPrefix(v, prefix)
	if !ok {
		return v, nil
	}
	parts := strings.Split(body, ":")
	if len(parts) != 3 {
		return "", errors.New("malformed encrypted value: want enc:v1:<iv>:<ciphertext>:<tag>")
	}
	iv, err1 := hex.DecodeString(parts[0])
	ct, err2 := hex.DecodeString(parts[1])
	tag, err3 := hex.DecodeString(parts[2])
	if err := errors.Join(err1, err2, err3); err != nil {
		return "", fmt.Errorf("malformed encrypted value: %w", err)
	}
	if len(iv) != 16 || len(tag) != 16 {
		return "", errors.New("malformed encrypted value: want a 16-byte IV and a 16-byte tag")
	}
	sealed := append(append(make([]byte, 0, len(ct)+len(tag)), ct...), tag...)
	for _, k := range d.keys {
		block, err := aes.NewCipher(k)
		if err != nil {
			return "", err
		}
		gcm, err := cipher.NewGCMWithNonceSize(block, 16)
		if err != nil {
			return "", err
		}
		if plain, err := gcm.Open(nil, iv, sealed, nil); err == nil {
			return string(plain), nil
		}
	}
	return "", ErrUndecryptable
}

var quoted = regexp.MustCompile(`^["'](.*)["']$`)

// LoadSecret lists the STORAGE_ENCRYPTION_KEY values to try, in order: the environment variable, then the
// STORAGE_ENCRYPTION_KEY= line of <dataDir>/server.env, then of <dataDir>/.env. For a file value, quotes around it
// are stripped; when it contains "=", the forms OmniRoute's own loader produces (it keeps only the text up to the
// second "=" of the line: split("=", 2)[1]) follow, with and without a leftover opening quote. Missing files are
// skipped.
func LoadSecret(dataDir string, env func(string) (string, bool)) ([]string, error) {
	var cands []string
	add := func(v string) {
		if v != "" && !slices.Contains(cands, v) {
			cands = append(cands, v)
		}
	}
	if v, ok := env(secretVar); ok {
		add(v)
	}
	for _, name := range []string{"server.env", ".env"} {
		rest, ok, err := envFileValue(filepath.Join(dataDir, name))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		add(quoted.ReplaceAllString(strings.TrimSpace(rest), "$1"))
		if head, _, cut := strings.Cut(rest, "="); cut {
			js := quoted.ReplaceAllString(strings.TrimSpace(head), "$1") // exactly what OmniRoute's loader yields
			add(strings.TrimLeft(js, `"'`))
			add(js)
		}
	}
	return cands, nil
}

// envFileValue returns the text after "STORAGE_ENCRYPTION_KEY=" on the first such line of an env file.
func envFileValue(path string) (string, bool, error) {
	f, err := os.Open(path) //nolint:gosec // G304: a file in the data directory the operator names.
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, secretVar+"="); ok {
			return rest, true, nil
		}
	}
	return "", false, sc.Err()
}

// PickSecret returns a Decrypter for the first candidate that opens probe, any encrypted value from the database.
func PickSecret(cands []string, probe string) (*Decrypter, error) {
	for _, c := range cands {
		d := NewDecrypter(c)
		if _, err := d.Decrypt(probe); err == nil {
			return d, nil
		}
	}
	return nil, errors.New("STORAGE_ENCRYPTION_KEY does not decrypt this database")
}
