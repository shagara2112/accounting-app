// Package web menyisipkan hasil build frontend (web/dist) ke dalam binary Lajur.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist mengembalikan isi folder dist sebagai root.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
