// Package money menyimpan nominal uang sebagai bilangan bulat berskala 10^4, sama dengan
// kolom NUMERIC(19,4) di basis data. Tidak ada floating point di mana pun, sehingga
// debit = kredit selalu bisa dibandingkan persis.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// Scale adalah jumlah angka desimal yang disimpan.
const Scale = 4

var (
	unit    = big.NewInt(10_000)
	ten     = big.NewInt(10)
	pattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,14})(\.[0-9]{1,4})?$`)
)

// Amount bernilai tetap (immutable); nilai nol siap dipakai.
type Amount struct{ u *big.Int } // nominal × 10^4; nil berarti nol

var ErrInvalid = errors.New("nominal tidak valid")

// Parse membaca "1250000", "1250000.5", atau "-12.3456". Maksimal 15 angka bulat dan
// 4 angka desimal, tanpa pemisah ribuan; format lain ditolak.
func Parse(s string) (Amount, error) {
	if !pattern.MatchString(s) {
		return Amount{}, fmt.Errorf("%w: %q", ErrInvalid, s)
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	frac += strings.Repeat("0", Scale-len(frac))
	u, _ := new(big.Int).SetString(whole+frac, 10)
	if neg {
		u.Neg(u)
	}
	return Amount{u: u}, nil
}

// MustParse untuk konstanta di kode dan tes.
func MustParse(s string) Amount {
	a, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return a
}

// FromInt membuat nominal rupiah bulat.
func FromInt(n int64) Amount {
	return Amount{u: new(big.Int).Mul(big.NewInt(n), unit)}
}

func (a Amount) units() *big.Int {
	if a.u == nil {
		return new(big.Int)
	}
	return a.u
}

func (a Amount) Add(b Amount) Amount { return Amount{u: new(big.Int).Add(a.units(), b.units())} }
func (a Amount) Sub(b Amount) Amount { return Amount{u: new(big.Int).Sub(a.units(), b.units())} }
func (a Amount) Neg() Amount         { return Amount{u: new(big.Int).Neg(a.units())} }
func (a Amount) Abs() Amount         { return Amount{u: new(big.Int).Abs(a.units())} }
func (a Amount) Sign() int           { return a.units().Sign() }
func (a Amount) IsZero() bool        { return a.Sign() == 0 }
func (a Amount) Cmp(b Amount) int    { return a.units().Cmp(b.units()) }
func (a Amount) Equal(b Amount) bool { return a.Cmp(b) == 0 }

// String selalu dengan 4 angka desimal, mis. "-1250000.0000"; format ini yang dikirim lewat API.
func (a Amount) String() string {
	u := a.units()
	digits := new(big.Int).Abs(u).String()
	if len(digits) <= Scale {
		digits = strings.Repeat("0", Scale-len(digits)+1) + digits
	}
	sign := ""
	if u.Sign() < 0 {
		sign = "-"
	}
	return sign + digits[:len(digits)-Scale] + "." + digits[len(digits)-Scale:]
}

// IDR menulis nominal untuk dibaca orang, mis. "Rp 1.250.000" atau "Rp 1.250.000,50".
func (a Amount) IDR() string {
	s := a.Abs().String()
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if frac = strings.TrimRight(frac, "0"); frac != "" {
		if len(frac) < 2 {
			frac += "0"
		}
		b.WriteString("," + frac)
	}
	if a.Sign() < 0 {
		return "-Rp " + b.String()
	}
	return "Rp " + b.String()
}

// MarshalJSON menulis nominal sebagai string desimal, tidak pernah sebagai angka JSON.
func (a Amount) MarshalJSON() ([]byte, error) { return json.Marshal(a.String()) }

func (a *Amount) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("%w: nominal harus dikirim sebagai string", ErrInvalid)
	}
	v, err := Parse(s)
	if err != nil {
		return err
	}
	*a = v
	return nil
}

// ScanNumeric dipakai pgx saat membaca kolom NUMERIC. Nilai yang punya lebih dari
// 4 angka desimal ditolak, bukan dibulatkan diam-diam.
func (a *Amount) ScanNumeric(v pgtype.Numeric) error {
	if !v.Valid {
		return fmt.Errorf("%w: NULL", ErrInvalid)
	}
	if v.NaN || v.InfinityModifier != pgtype.Finite {
		return fmt.Errorf("%w: NaN atau tak hingga", ErrInvalid)
	}
	u := new(big.Int).Set(v.Int)
	shift := int64(v.Exp) + Scale
	switch {
	case shift > 0:
		u.Mul(u, new(big.Int).Exp(ten, big.NewInt(shift), nil))
	case shift < 0:
		var rem big.Int
		u.QuoRem(u, new(big.Int).Exp(ten, big.NewInt(-shift), nil), &rem)
		if rem.Sign() != 0 {
			return fmt.Errorf("%w: lebih dari %d angka desimal", ErrInvalid, Scale)
		}
	}
	a.u = u
	return nil
}

// NumericValue dipakai pgx saat menulis parameter NUMERIC.
func (a Amount) NumericValue() (pgtype.Numeric, error) {
	return pgtype.Numeric{Int: new(big.Int).Set(a.units()), Exp: -Scale, Valid: true}, nil
}
