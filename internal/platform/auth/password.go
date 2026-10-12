// Package auth menangani kata sandi, sesi, login, dan logout.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

var (
	ErrWeakPassword = errors.New("kata sandi minimal 10 karakter")
	errBadHash      = errors.New("format hash kata sandi tidak dikenal")
)

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
}

var params = argonParams{memory: 64 * 1024, time: 3, threads: 2, keyLen: 32}

// HashPassword menghasilkan hash Argon2id dalam format PHC.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < 10 {
		return "", ErrWeakPassword
	}
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, params.keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.memory, params.time, params.threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword membandingkan kata sandi dengan hash dalam waktu konstan.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// burnTime menyamakan waktu respons untuk email yang tidak terdaftar,
// supaya penyerang tidak bisa menebak email mana yang ada.
func burnTime(password string) {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword("lajur-dummy-password") })
	_, _ = VerifyPassword(password, dummyHash)
}
