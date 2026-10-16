//go:build !release

package license

import (
	"context"
	"fmt"
	"time"
)

// trustDevKeys: build pengembangan mempercayai kunci License Console tiruan (keys/dev).
const trustDevKeys = true

// DevBuild: build pengembangan (boleh memakai License Console tiruan di http://localhost).
const DevBuild = true

// local mensimulasikan lisensi untuk pengembangan, seperti provider LOCAL di pos-app.
// Build rilis (tag release) tidak menyertakan file ini, sehingga mode local ditolak.
type local struct {
	state   State
	expires string
	now     func() time.Time
}

func newLocal(s Settings) (Provider, error) {
	st := State(s.LocalState)
	switch st {
	case Active, Warning, Expired, Suspended, Unverified, InvalidKey:
	default:
		return nil, fmt.Errorf("LAJUR_LICENSE_LOCAL_STATUS tidak dikenal: %q", s.LocalState)
	}
	return &local{state: st, expires: s.LocalExpires, now: time.Now}, nil
}

func (l *local) Status(context.Context) Status {
	return Evaluate(l.state, l.expires, 7, l.now(), jakarta)
}
