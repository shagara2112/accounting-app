package secret

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestSealOpenRoundTripAndPurposeBinding(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed := b.Seal([]byte("JBSWY3DPEHPK3PXP"), "totp")
	if bytes.Contains(sealed, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatal("teks asli terlihat di hasil enkripsi")
	}
	got, err := b.Open(sealed, "totp")
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := b.Open(sealed, "license"); err != ErrCorrupt {
		t.Fatalf("membuka dengan purpose lain harus gagal, dapat %v", err)
	}
	other, _ := New(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Open(sealed, "totp"); err != ErrCorrupt {
		t.Fatalf("membuka dengan kunci lain harus gagal, dapat %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := b.Open(sealed, "totp"); err != ErrCorrupt {
		t.Fatalf("data yang diubah harus ditolak, dapat %v", err)
	}
}

func TestParseKey(t *testing.T) {
	raw := bytes.Repeat([]byte{1}, 32)
	for _, s := range []string{base64.StdEncoding.EncodeToString(raw), base64.RawURLEncoding.EncodeToString(raw)} {
		if k, err := ParseKey(s); err != nil || !bytes.Equal(k, raw) {
			t.Fatalf("ParseKey(%q) = %v, %v", s, k, err)
		}
	}
	if _, err := ParseKey(base64.StdEncoding.EncodeToString(raw[:16])); err == nil {
		t.Fatal("kunci 16 byte harus ditolak")
	}
}
