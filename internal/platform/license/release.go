//go:build release

package license

import "fmt"

// trustDevKeys: build rilis hanya mempercayai kunci produksi di keys/release.
const trustDevKeys = false

// DevBuild: build rilis tidak pernah menerima License Console lewat http.
const DevBuild = false

func newLocal(Settings) (Provider, error) {
	return nil, fmt.Errorf("provider lisensi local tidak boleh dipakai di build rilis")
}
