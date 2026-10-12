package audit

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestCanonicalIsStableAcrossJSONB(t *testing.T) {
	type doc struct {
		Total string `json:"total"`
		Akun  string `json:"akun"`
		Baris int    `json:"baris"`
	}
	fromStruct, err := canonical(doc{Total: "1250000.0000", Akun: "1-1300", Baris: 3})
	if err != nil {
		t.Fatal(err)
	}
	// JSONB mengembalikan kunci dengan urutan dan spasi yang berbeda.
	fromDB, err := canonical(json.RawMessage(`{"akun": "1-1300", "baris": 3, "total": "1250000.0000"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fromStruct, fromDB) {
		t.Fatalf("hasil kanonis berbeda:\n%s\n%s", fromStruct, fromDB)
	}
}

func TestHashChangesWhenAnyFieldChanges(t *testing.T) {
	base := record{At: time.Date(2027, 1, 4, 9, 0, 0, 0, time.UTC), Action: "auth.login", ObjectType: "user", ObjectID: "u1"}
	h := base.hash(genesis)
	changed := base
	changed.Action = "auth.logout"
	if bytes.Equal(h, changed.hash(genesis)) {
		t.Fatal("hash tidak berubah saat action berubah")
	}
	if bytes.Equal(h, base.hash([]byte("lain"))) {
		t.Fatal("hash tidak bergantung pada hash sebelumnya")
	}
}
