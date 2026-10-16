package auth

import (
	"strings"
	"testing"
	"time"
)

// Vektor uji RFC 6238 lampiran B untuk SHA1 (8 digit dipotong menjadi 6 digit terakhir).
func TestTOTPMatchesRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"}
	for unix, want := range cases {
		if got := totpCode(secret, totpStep(time.Unix(unix, 0))); got != want {
			t.Errorf("t=%d: %s, seharusnya %s", unix, got, want)
		}
	}
}

func TestVerifyTOTPWindowAndReplay(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	cur := totpStep(now)
	step, ok := VerifyTOTP(secret, totpCode(secret, cur-1), now, 0)
	if !ok || step != cur-1 {
		t.Fatalf("kode langkah sebelumnya harus diterima: %d %v", step, ok)
	}
	if _, ok := VerifyTOTP(secret, totpCode(secret, cur-2), now, 0); ok {
		t.Fatal("kode dua langkah lalu harus ditolak")
	}
	if _, ok := VerifyTOTP(secret, totpCode(secret, cur), now, cur); ok {
		t.Fatal("kode yang langkahnya sudah dipakai harus ditolak")
	}
	if _, ok := VerifyTOTP(secret, "050 471", now, 0); !ok {
		t.Fatal("spasi di kode harus diabaikan")
	}
	if _, ok := VerifyTOTP(secret, "12345", now, 0); ok {
		t.Fatal("kode 5 digit harus ditolak")
	}
}

func TestTOTPKeyAndURI(t *testing.T) {
	secret := []byte("12345678901234567890")
	if got := TOTPKey(secret); got != "GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ" {
		t.Fatalf("kunci = %q", got)
	}
	uri := TOTPURI("Lajur", "budi@arta.test", secret)
	if !strings.HasPrefix(uri, "otpauth://totp/Lajur:budi@arta.test?") || !strings.Contains(uri, "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ") {
		t.Fatalf("uri = %s", uri)
	}
}
