package auth

import (
	"errors"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("rahasia-lajur-2027")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword("rahasia-lajur-2027", h); !ok {
		t.Fatal("kata sandi benar ditolak")
	}
	if ok, _ := VerifyPassword("rahasia-lajur-2028", h); ok {
		t.Fatal("kata sandi salah diterima")
	}
}

func TestHashIsSalted(t *testing.T) {
	a, _ := HashPassword("rahasia-lajur-2027")
	b, _ := HashPassword("rahasia-lajur-2027")
	if a == b {
		t.Fatal("dua hash kata sandi yang sama identik; salt tidak dipakai")
	}
}

func TestRejectsShortPassword(t *testing.T) {
	if _, err := HashPassword("pendek"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("err = %v, seharusnya ErrWeakPassword", err)
	}
}
