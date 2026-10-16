// Package backup membuat backup terjadwal yang terenkripsi (PLT-09), menyalinnya ke folder
// lokal, folder lain (NAS), dan penyimpanan S3-compatible, lalu memulihkannya.
//
// Enkripsi memakai kunci publik: server hanya memegang kunci publik penerima, sedangkan kunci
// privatnya diserahkan ke Pemilik saat instalasi. Tanpa kunci privat itu, file backup tidak
// bisa dibaca siapa pun, termasuk orang yang menguasai server.
package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Format file (lajur-backup v1):
//
//	"LAJURBK1" | kunci publik efemeral X25519 (32) | id kunci penerima (8) | potongan…
//	potongan  = panjang ciphertext uint32 BE | AES-256-GCM(≤ 64 KiB)
//
// Kunci AES = HKDF-SHA256(X25519(efemeral, penerima), salt = pub efemeral ‖ pub penerima).
// Nonce potongan ke-i = i (11 byte) ‖ penanda potongan terakhir, sehingga file yang terpotong
// atau potongannya ditukar ketahuan. Header ikut diautentikasi sebagai associated data.
const (
	magic       = "LAJURBK1"
	chunkSize   = 64 << 10
	privPrefix  = "lajur-backup-key-1:"
	pubPrefix   = "lajur-backup-pub-1:"
	headerSize  = len(magic) + 32 + 8
	hkdfContext = "lajur-backup-v1"
)

var ErrCorrupt = errors.New("file backup rusak, terpotong, atau bukan untuk kunci ini")

// NewKey membuat pasangan kunci backup. Kunci privat diserahkan ke Pemilik; kunci publik
// disimpan di server (LAJUR_BACKUP_PUBLIC_KEY).
func NewKey() (priv, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return privPrefix + base64.RawURLEncoding.EncodeToString(k.Bytes()),
		pubPrefix + base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

func ParsePublicKey(s string) (*ecdh.PublicKey, error) {
	raw, err := decodeKey(s, pubPrefix)
	if err != nil {
		return nil, err
	}
	return ecdh.X25519().NewPublicKey(raw)
}

func ParsePrivateKey(s string) (*ecdh.PrivateKey, error) {
	raw, err := decodeKey(s, privPrefix)
	if err != nil {
		return nil, err
	}
	return ecdh.X25519().NewPrivateKey(raw)
}

func decodeKey(s, prefix string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, prefix) {
		return nil, fmt.Errorf("kunci harus diawali %q", prefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, prefix))
	if err != nil || len(raw) != 32 {
		return nil, errors.New("isi kunci tidak valid")
	}
	return raw, nil
}

// KeyID adalah sidik kunci publik (8 byte pertama SHA-256), ditampilkan di halaman Sistem
// supaya Pemilik bisa mencocokkannya dengan kunci yang ia simpan.
func KeyID(pub *ecdh.PublicKey) string {
	sum := sha256.Sum256(pub.Bytes())
	h := hex.EncodeToString(sum[:8])
	return h[:4] + "-" + h[4:8] + "-" + h[8:12] + "-" + h[12:]
}

func deriveKey(shared, ephPub, recipient []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, shared, append(append([]byte{}, ephPub...), recipient...), hkdfContext, 32)
}

func nonce(i uint64, last bool) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[3:11], i)
	if last {
		n[11] = 1
	}
	return n
}

// Encrypt menulis isi r dalam format terenkripsi ke w untuk penerima pub.
func Encrypt(w io.Writer, r io.Reader, pub *ecdh.PublicKey) error {
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return err
	}
	key, err := deriveKey(shared, eph.PublicKey().Bytes(), pub.Bytes())
	if err != nil {
		return err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(pub.Bytes())
	header := append(append([]byte(magic), eph.PublicKey().Bytes()...), sum[:8]...)
	if _, err := w.Write(header); err != nil {
		return err
	}

	// Baca satu potongan ke depan supaya potongan terakhir bisa ditandai.
	cur := make([]byte, chunkSize)
	next := make([]byte, chunkSize)
	n, err := io.ReadFull(r, cur)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return err
	}
	var lenBuf [4]byte
	for i := uint64(0); ; i++ {
		var m int
		last := n < chunkSize
		if !last {
			m, err = io.ReadFull(r, next)
			if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
				return err
			}
			last = m == 0
		}
		ct := aead.Seal(nil, nonce(i, last), cur[:n], header)
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ct)))
		if _, err := w.Write(lenBuf[:]); err != nil {
			return err
		}
		if _, err := w.Write(ct); err != nil {
			return err
		}
		if last {
			return nil
		}
		cur, next, n = next, cur, m
	}
}

// Decrypt membuka file terenkripsi dengan kunci privat Pemilik dan menulis isinya ke w.
func Decrypt(w io.Writer, r io.Reader, priv *ecdh.PrivateKey) error {
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(r, header); err != nil || string(header[:len(magic)]) != magic {
		return errors.New("bukan file backup Lajur")
	}
	sum := sha256.Sum256(priv.PublicKey().Bytes())
	if !bytes.Equal(header[len(magic)+32:], sum[:8]) {
		return fmt.Errorf("file ini dienkripsi untuk kunci lain (%s), bukan kunci ini", formatKeyID(header[len(magic)+32:]))
	}
	ephPub, err := ecdh.X25519().NewPublicKey(header[len(magic) : len(magic)+32])
	if err != nil {
		return ErrCorrupt
	}
	shared, err := priv.ECDH(ephPub)
	if err != nil {
		return ErrCorrupt
	}
	key, err := deriveKey(shared, ephPub.Bytes(), priv.PublicKey().Bytes())
	if err != nil {
		return err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	var lenBuf [4]byte
	buf := make([]byte, chunkSize+aead.Overhead())
	for i := uint64(0); ; i++ {
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return ErrCorrupt // berakhir sebelum potongan terakhir
		}
		size := binary.BigEndian.Uint32(lenBuf[:])
		if size < uint32(aead.Overhead()) || size > uint32(len(buf)) {
			return ErrCorrupt
		}
		ct := buf[:size]
		if _, err := io.ReadFull(r, ct); err != nil {
			return ErrCorrupt
		}
		plain, err := aead.Open(nil, nonce(i, false), ct, header)
		last := false
		if err != nil {
			if plain, err = aead.Open(nil, nonce(i, true), ct, header); err != nil {
				return ErrCorrupt
			}
			last = true
		}
		if _, err := w.Write(plain); err != nil {
			return err
		}
		if last {
			if n, _ := r.Read(lenBuf[:1]); n != 0 {
				return ErrCorrupt // ada data setelah potongan terakhir
			}
			return nil
		}
	}
}

func formatKeyID(b []byte) string {
	h := hex.EncodeToString(b)
	return h[:4] + "-" + h[4:8] + "-" + h[8:12] + "-" + h[12:]
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
