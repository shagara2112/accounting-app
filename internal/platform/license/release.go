//go:build release

package license

import "fmt"

func newLocal(Settings) (Provider, error) {
	return nil, fmt.Errorf("provider lisensi local tidak boleh dipakai di build rilis")
}
