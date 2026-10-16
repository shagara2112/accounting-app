package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP mengikuti RFC 6238 dengan parameter yang didukung semua aplikasi autentikator:
// HMAC-SHA1, 6 digit, langkah 30 detik. Kode dari satu langkah sebelum dan sesudahnya
// tetap diterima untuk menoleransi selisih jam ponsel.
const (
	totpPeriod = 30
	totpDigits = 6
	totpSkew   = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret membuat rahasia acak 160 bit.
func NewTOTPSecret() []byte {
	s := make([]byte, 20)
	_, _ = rand.Read(s)
	return s
}

// TOTPKey menampilkan rahasia dalam base32 berkelompok empat huruf, untuk diketik manual.
func TOTPKey(secret []byte) string {
	raw := b32.EncodeToString(secret)
	var parts []string
	for i := 0; i < len(raw); i += 4 {
		parts = append(parts, raw[i:min(i+4, len(raw))])
	}
	return strings.Join(parts, " ")
}

// TOTPURI adalah tautan otpauth:// yang dikenali aplikasi autentikator.
func TOTPURI(issuer, account string, secret []byte) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", b32.EncodeToString(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000)
}

// VerifyTOTP mengembalikan langkah waktu kode yang cocok. Langkah yang sudah pernah
// dipakai (≤ after) ditolak, sehingga kode yang sama tidak bisa dipakai ulang.
func VerifyTOTP(secret []byte, code string, now time.Time, after int64) (int64, bool) {
	code = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := totpStep(now)
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		step := cur + d
		if step <= after {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// TOTPAt menghasilkan kode untuk waktu t; dipakai tes dan alat bantu dukungan.
func TOTPAt(secret []byte, t time.Time) string { return totpCode(secret, totpStep(t)) }

// ParseTOTPKey membaca kunci base32 yang ditampilkan TOTPKey (spasi diabaikan).
func ParseTOTPKey(key string) ([]byte, error) {
	return b32.DecodeString(strings.ToUpper(strings.ReplaceAll(key, " ", "")))
}
