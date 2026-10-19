package coretax

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// Referensi bukti potong Coretax: kode objek pajak (KOP) beserta tarifnya, kode fasilitas,
// jenis dokumen referensi, dan kode negara. Diambil dari sheet REF converter Excel resmi DJP
// (lihat field source di bupot.json). Bila DJP memperbarui converter, perbarui berkas ini.
//
//go:embed refdata/bupot.json
var bupotJSON []byte

type TaxObject struct {
	Code   string    `json:"code"`
	Name   string    `json:"name"`
	Rate   float64   `json:"rate"`             // persen, mis. 2 atau 2.65
	Deemed []float64 `json:"deemed,omitempty"` // hanya BPNR: norma penghasilan neto (%)
}

type RefItem struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type BupotRef struct {
	Source         string      `json:"source"`
	BPPUObjects    []TaxObject `json:"bppuObjects"`
	BPNRObjects    []TaxObject `json:"bpnrObjects"`
	BPPUFacilities []RefItem   `json:"bppuFacilities"`
	BPNRFacilities []RefItem   `json:"bpnrFacilities"`
	Documents      []RefItem   `json:"documents"`
	Countries      []RefItem   `json:"countries"`
}

var (
	refOnce sync.Once
	ref     BupotRef
)

// Ref mengembalikan referensi bukti potong (dibaca sekali dari berkas tertanam).
func Ref() BupotRef {
	refOnce.Do(func() {
		if err := json.Unmarshal(bupotJSON, &ref); err != nil {
			panic("coretax: refdata/bupot.json rusak: " + err.Error())
		}
	})
	return ref
}

func findObject(list []TaxObject, code string) (TaxObject, bool) {
	for _, o := range list {
		if o.Code == code {
			return o, true
		}
	}
	return TaxObject{}, false
}

func hasItem(list []RefItem, code string) bool {
	for _, it := range list {
		if it.Code == code {
			return true
		}
	}
	return false
}
