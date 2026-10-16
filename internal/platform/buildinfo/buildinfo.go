// Package buildinfo menyimpan versi aplikasi. Build rilis mengisinya lewat
// -ldflags "-X lajur/internal/platform/buildinfo.Version=1.2.3".
package buildinfo

import (
	"strconv"
	"strings"
)

var Version = "0.8.0-dev"

// Less membandingkan dua versi semantik (mayor.minor.patch); akhiran seperti -dev atau -rc1
// diabaikan, dan bagian yang tidak ada dianggap nol.
func Less(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
