package license

import (
	"context"
	"fmt"
	"time"
)

// Jakarta dipakai sebagai zona waktu evaluasi lisensi sampai License Console mengirim zona waktu per client.
var jakarta = mustLoad("Asia/Jakarta")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("WIB", 7*60*60)
	}
	return loc
}

// Settings memilih provider lisensi.
type Settings struct {
	Mode         string // "local" atau "remote"
	LocalState   string
	LocalExpires string
}

// New memilih provider sesuai Settings. Provider "local" hanya ada di build pengembangan.
func New(s Settings) (Provider, error) {
	switch s.Mode {
	case "local":
		return newLocal(s)
	case "remote":
		return nil, fmt.Errorf("provider lisensi remote (License Console) belum dibangun; dijadwalkan di workstream lisensi")
	}
	return nil, fmt.Errorf("LAJUR_LICENSE_MODE tidak dikenal: %q", s.Mode)
}

// Static mengembalikan status yang sama setiap saat; berguna untuk tes.
type Static struct{ S Status }

func (s Static) Status(context.Context) Status { return s.S }
