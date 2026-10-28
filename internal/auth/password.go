package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters for stored passwords. Memory cost dominates:
// one hash holds 64 MiB briefly, which bounds how fast passwords can
// be tried on this machine and keeps interactive sign-in under a
// second. Stored hashes name these parameters, and verification
// requires them exactly: anything else fails closed.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
)

// GeneratedPasswordLength is the bootstrap password length: 20 is the
// floor, 24 from a 50-character alphabet is ~135 bits.
const GeneratedPasswordLength = 24

// passwordAlphabet drops look-alike characters (0/O, 1/l/I, 5/S, 2/Z
// and lowercase o) so a printed password cannot be misread.
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRTUVWXY346789"

// GeneratePassword returns a random password from passwordAlphabet.
// The 256-to-50 modulo bias is negligible for a printed secret.
func GeneratePassword() (string, error) {
	buf := make([]byte, GeneratedPasswordLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: cannot generate password: %w", err)
	}
	var sb strings.Builder
	for _, b := range buf {
		sb.WriteByte(passwordAlphabet[int(b)%len(passwordAlphabet)])
	}
	return sb.String(), nil
}

// HashPassword hashes password with argon2id and returns a PHC string
// carrying the parameters above.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: cannot salt password: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

// CheckPassword reports whether password matches the stored PHC hash.
// The comparison is constant-time; anything unparseable, or carrying
// parameters other than ours, fails closed.
func CheckPassword(encoded, password string) bool {
	salt, expected, ok := parsePasswordHash(encoded)
	if !ok {
		return false
	}
	candidate := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return subtle.ConstantTimeCompare(candidate, expected) == 1
}

// parsePasswordHash extracts the salt and hash, requiring exactly our
// algorithm, version, and parameters. Hashes are written by
// HashPassword only, so strictness costs nothing and a corrupt row
// can never pick weaker parameters.
func parsePasswordHash(encoded string) (salt, expected []byte, ok bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, false
	}
	var version, memory, time, threads uint32
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return nil, nil, false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return nil, nil, false
	}
	if memory != argonMemory || time != argonTime || threads != argonThreads {
		return nil, nil, false
	}
	var err error
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(salt) != saltLen {
		return nil, nil, false
	}
	if expected, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(expected) != argonKeyLen {
		return nil, nil, false
	}
	return salt, expected, true
}

// dummyHashOnce holds a valid hash of a password nobody knows, computed
// once. Unknown usernames verify against it so they cost the same as a
// wrong password: response time reveals nothing about whether the
// username exists.
var dummyHashOnce = sync.OnceValue(func() string {
	hash, err := HashPassword("auth-dummy-password-nobody-knows")
	if err != nil {
		return "invalid"
	}
	return hash
})

// CheckDummyPassword burns one argon2id verification for an unknown
// username. The result is discarded: it exists only for its duration.
func CheckDummyPassword() {
	CheckPassword(dummyHashOnce(), "auth-dummy-wrong-password")
}
