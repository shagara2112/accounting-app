// Package civil berisi tanggal kalender tanpa jam (kolom DATE), mis. tanggal jurnal.
// Tanggal tidak pernah bergeser karena zona waktu: 31 Oktober tetap 31 Oktober.
package civil

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Date disimpan sebagai tengah malam UTC; nilai nol berarti belum diisi.
type Date struct{ t time.Time }

// Jakarta adalah zona waktu default untuk menentukan "hari ini".
var Jakarta = mustLoad("Asia/Jakarta")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func New(y int, m time.Month, d int) Date { return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)} }

// Of mengambil tanggal dari t di zona waktu t sendiri.
func Of(t time.Time) Date { return New(t.Year(), t.Month(), t.Day()) }

// Today adalah tanggal hari ini di loc.
func Today(loc *time.Location) Date { return Of(time.Now().In(loc)) }

// Parse membaca "2006-01-02".
func Parse(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("tanggal %q tidak valid (format yyyy-mm-dd)", s)
	}
	return Date{t}, nil
}

// ParseMonth membaca "2006-01" dan mengembalikan tanggal 1 bulan itu.
func ParseMonth(s string) (Date, error) {
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return Date{}, fmt.Errorf("bulan %q tidak valid (format yyyy-mm)", s)
	}
	return Date{t}, nil
}

func (d Date) IsZero() bool          { return d.t.IsZero() }
func (d Date) Year() int             { return d.t.Year() }
func (d Date) Month() time.Month     { return d.t.Month() }
func (d Date) Day() int              { return d.t.Day() }
func (d Date) Before(o Date) bool    { return d.t.Before(o.t) }
func (d Date) After(o Date) bool     { return d.t.After(o.t) }
func (d Date) String() string        { return d.t.Format(time.DateOnly) }
func (d Date) MonthKey() string      { return d.t.Format("2006-01") }
func (d Date) AddDays(n int) Date    { return Date{d.t.AddDate(0, 0, n)} }
func (d Date) MonthStart() Date      { return New(d.Year(), d.Month(), 1) }
func (d Date) MonthEnd() Date        { return d.MonthStart().AddMonths(1).AddDays(-1) }
func (d Date) SameMonth(o Date) bool { return d.Year() == o.Year() && d.Month() == o.Month() }
func (d Date) Compare(o Date) int    { return d.t.Compare(o.t) }
func (d Date) AddMonths(n int) Date  { return Date{d.MonthStart().t.AddDate(0, n, 0)} } // hasil selalu tanggal 1

// Indonesian menulis tanggal untuk pesan ke pengguna, mis. "31 Oktober 2026".
func (d Date) Indonesian() string {
	return fmt.Sprintf("%d %s %d", d.Day(), MonthName(d.Month()), d.Year())
}

// MonthLabel menulis bulan untuk pesan ke pengguna, mis. "Oktober 2026".
func (d Date) MonthLabel() string { return MonthName(d.Month()) + " " + fmt.Sprint(d.Year()) }

var months = [...]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli",
	"Agustus", "September", "Oktober", "November", "Desember"}

func MonthName(m time.Month) string { return months[m-1] }

func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New("tanggal harus berupa string yyyy-mm-dd")
	}
	v, err := Parse(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// ScanDate dipakai pgx saat membaca kolom DATE.
func (d *Date) ScanDate(v pgtype.Date) error {
	if !v.Valid || v.InfinityModifier != pgtype.Finite {
		return errors.New("tanggal kosong atau tak hingga")
	}
	*d = Of(v.Time)
	return nil
}

// DateValue dipakai pgx saat menulis parameter DATE.
func (d Date) DateValue() (pgtype.Date, error) {
	if d.IsZero() {
		return pgtype.Date{}, nil
	}
	return pgtype.Date{Time: d.t, Valid: true}, nil
}

// DaysSince adalah jumlah hari dari o ke d (negatif bila d sebelum o).
func (d Date) DaysSince(o Date) int { return int(d.t.Sub(o.t).Hours() / 24) }
