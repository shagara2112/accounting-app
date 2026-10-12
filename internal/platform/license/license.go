// Package license mengevaluasi status lisensi dan menentukan request mana yang boleh jalan.
package license

import (
	"context"
	"fmt"
	"time"
)

type State string

const (
	Active     State = "ACTIVE"
	Warning    State = "WARNING"
	Expired    State = "EXPIRED"
	Suspended  State = "SUSPENDED"
	Unverified State = "UNVERIFIED"
	InvalidKey State = "INVALID_KEY"
)

// Mode adalah kebutuhan lisensi sebuah route. Setiap route wajib menyatakannya.
type Mode int

const (
	ModeUnset  Mode = iota
	ModeStatus      // selalu boleh: login, status lisensi
	ModeRead        // baca data
	ModeExport      // ekspor data: tidak pernah dikunci lisensi (PRD)
	ModeWrite       // mengubah data
)

type Status struct {
	State     State  `json:"status"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	DaysLeft  *int   `json:"daysLeft,omitempty"`
	Message   string `json:"message"`
}

// Allows menerapkan tabel "Perilaku aplikasi per kondisi lisensi" di PRD.
func (s Status) Allows(m Mode) bool {
	switch m {
	case ModeStatus, ModeExport:
		return true
	case ModeRead:
		return s.State != Suspended && s.State != InvalidKey
	case ModeWrite:
		return s.State == Active || s.State == Warning
	}
	return false
}

type Provider interface {
	Status(ctx context.Context) Status
}

// Evaluate menghitung status efektif dari status tersimpan dan tanggal expired (inklusif).
func Evaluate(stored State, expiresAt string, warningDays int, now time.Time, tz *time.Location) Status {
	if stored != Active || expiresAt == "" {
		return Status{State: stored, ExpiresAt: expiresAt, Message: message(stored, nil)}
	}
	exp, err := time.ParseInLocation("2006-01-02", expiresAt, tz)
	if err != nil {
		return Status{State: InvalidKey, Message: "Tanggal expired lisensi tidak valid."}
	}
	today := time.Date(now.In(tz).Year(), now.In(tz).Month(), now.In(tz).Day(), 0, 0, 0, 0, tz)
	days := int(exp.Sub(today).Hours() / 24)
	st := Active
	switch {
	case days < 0:
		st = Expired
	case days <= warningDays:
		st = Warning
	}
	return Status{State: st, ExpiresAt: expiresAt, DaysLeft: &days, Message: message(st, &days)}
}

func message(s State, days *int) string {
	switch s {
	case Active:
		return "Lisensi aktif."
	case Warning:
		return fmt.Sprintf("Masa aktif lisensi berakhir dalam %d hari.", *days)
	case Expired:
		return "Masa aktif lisensi telah berakhir. Data tetap bisa dilihat dan diekspor."
	case Suspended:
		return "Lisensi ditangguhkan. Hubungi pemilik aplikasi."
	case Unverified:
		return "Lisensi belum bisa diverifikasi ke License Console."
	case InvalidKey:
		return "License key tidak valid."
	}
	return string(s)
}
