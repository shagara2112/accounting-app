package backup

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"strings"
	"testing"
)

func keys(t *testing.T) (string, string) {
	t.Helper()
	priv, pub, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

func seal(t *testing.T, plain []byte, pubStr string) []byte {
	t.Helper()
	pub, err := ParsePublicKey(pubStr)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Encrypt(&out, bytes.NewReader(plain), pub); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func open(sealed []byte, privStr string) ([]byte, error) {
	priv, err := ParsePrivateKey(privStr)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	err = Decrypt(&out, bytes.NewReader(sealed), priv)
	return out.Bytes(), err
}

func TestEncryptRoundTripAtChunkBoundaries(t *testing.T) {
	priv, pub := keys(t)
	for _, n := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 5} {
		plain := make([]byte, n)
		_, _ = rand.Read(plain)
		sealed := seal(t, plain, pub)
		if bytes.Contains(sealed, plain[:min(n, 64)]) && n >= 16 {
			t.Fatalf("n=%d: teks asli terlihat", n)
		}
		got, err := open(sealed, priv)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("n=%d: round trip gagal: %v", n, err)
		}
	}
}

func TestDecryptRejectsWrongKeyAndTampering(t *testing.T) {
	priv, pub := keys(t)
	other, _ := keys(t)
	plain := make([]byte, 2*chunkSize+100)
	_, _ = rand.Read(plain)
	sealed := seal(t, plain, pub)

	if _, err := open(sealed, other); err == nil || !strings.Contains(err.Error(), "kunci lain") {
		t.Fatalf("kunci lain harus ditolak dengan pesan jelas, dapat %v", err)
	}

	// Potongan terakhir dibuang: file terpotong harus ketahuan.
	firstTwo := headerSize + 2*(4+chunkSize+16)
	if _, err := open(sealed[:firstTwo], priv); err != ErrCorrupt {
		t.Fatalf("file terpotong: %v", err)
	}
	if _, err := open(append(append([]byte{}, sealed...), 0, 0, 0, 1), priv); err != ErrCorrupt {
		t.Fatalf("data tambahan setelah potongan terakhir: %v", err)
	}
	flipped := append([]byte{}, sealed...)
	flipped[headerSize+100] ^= 1
	if _, err := open(flipped, priv); err != ErrCorrupt {
		t.Fatalf("byte diubah: %v", err)
	}
	header := append([]byte{}, sealed...)
	header[10] ^= 1 // kunci efemeral di header
	if _, err := open(header, priv); err != ErrCorrupt {
		t.Fatalf("header diubah: %v", err)
	}

	// Dua potongan pertama ditukar.
	c1 := sealed[headerSize : headerSize+4+chunkSize+16]
	c2 := sealed[headerSize+4+chunkSize+16 : firstTwo]
	swapped := append(append(append(append([]byte{}, sealed[:headerSize]...), c2...), c1...), sealed[firstTwo:]...)
	if binary.BigEndian.Uint32(c1[:4]) != binary.BigEndian.Uint32(c2[:4]) {
		t.Fatal("ukuran potongan berbeda; tes tidak valid")
	}
	if _, err := open(swapped, priv); err != ErrCorrupt {
		t.Fatalf("potongan ditukar: %v", err)
	}
}

func TestKeyParsing(t *testing.T) {
	priv, pub := keys(t)
	if _, err := ParsePublicKey(priv); err == nil {
		t.Fatal("kunci privat tidak boleh diterima sebagai kunci publik")
	}
	p, err := ParsePublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if id := KeyID(p); len(id) != 19 || strings.Count(id, "-") != 3 {
		t.Fatalf("KeyID = %q", id)
	}
}
