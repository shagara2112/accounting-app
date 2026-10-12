// Package audit menulis audit trail berantai hash (PLT-06): setiap baris menyimpan hash
// baris sebelumnya, sehingga pengubahan atau penghapusan baris lama bisa dibuktikan.
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lajur/internal/platform/db"
	"lajur/internal/platform/store"
)

// genesis adalah prev_hash untuk baris pertama.
var genesis = make([]byte, sha256.Size)

type Entry struct {
	UserID     *uuid.UUID
	CompanyID  *uuid.UUID
	Action     string // mis. auth.login, ledger.journal.post
	ObjectType string
	ObjectID   string
	Before     any
	After      any
	IP         string
}

// Now bisa diganti di tes.
var Now = time.Now

// Write menambah satu baris audit di transaksi yang sedang berjalan.
// Advisory lock menjaga rantai tetap lurus walau ada beberapa penulis bersamaan.
func Write(ctx context.Context, q db.Querier, e Entry) (int64, error) {
	s := store.New(q)
	if err := s.LockAuditChain(ctx); err != nil {
		return 0, err
	}
	prev, err := s.LastAuditHash(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		prev = genesis
	} else if err != nil {
		return 0, err
	}

	before, err := canonical(e.Before)
	if err != nil {
		return 0, err
	}
	after, err := canonical(e.After)
	if err != nil {
		return 0, err
	}
	rec := record{
		At: Now().UTC().Truncate(time.Microsecond), UserID: e.UserID, CompanyID: e.CompanyID,
		Action: e.Action, ObjectType: e.ObjectType, ObjectID: e.ObjectID,
		Before: before, After: after, IP: e.IP,
	}
	return s.InsertAudit(ctx, store.InsertAuditParams{
		At: rec.At, UserID: rec.UserID, CompanyID: rec.CompanyID,
		Action: rec.Action, ObjectType: rec.ObjectType, ObjectID: strPtr(rec.ObjectID),
		Before: before, After: after, Ip: strPtr(rec.IP),
		PrevHash: prev, Hash: rec.hash(prev),
	})
}

// Result adalah hasil pemeriksaan rantai.
type Result struct {
	OK       bool  `json:"ok"`
	Count    int64 `json:"count"`
	BrokenAt int64 `json:"brokenAt,omitempty"` // seq baris pertama yang tidak cocok
}

// Verify menghitung ulang seluruh rantai dari awal.
func Verify(ctx context.Context, q db.Querier) (Result, error) {
	rows, err := store.New(q).AuditChain(ctx)
	if err != nil {
		return Result{}, err
	}
	prev := genesis
	for _, r := range rows {
		before, err := canonical(rawOrNil(r.Before))
		if err != nil {
			return Result{}, err
		}
		after, err := canonical(rawOrNil(r.After))
		if err != nil {
			return Result{}, err
		}
		rec := record{
			At: r.At, UserID: r.UserID, CompanyID: r.CompanyID,
			Action: r.Action, ObjectType: r.ObjectType, ObjectID: deref(r.ObjectID),
			Before: before, After: after, IP: deref(r.Ip),
		}
		if !bytes.Equal(r.PrevHash, prev) || !bytes.Equal(r.Hash, rec.hash(prev)) {
			return Result{OK: false, Count: int64(len(rows)), BrokenAt: r.Seq}, nil
		}
		prev = r.Hash
	}
	return Result{OK: true, Count: int64(len(rows))}, nil
}

type record struct {
	At                time.Time
	UserID, CompanyID *uuid.UUID
	Action            string
	ObjectType        string
	ObjectID          string
	Before, After     []byte
	IP                string
}

func (r record) hash(prev []byte) []byte {
	h := sha256.New()
	sep := []byte{0x1f}
	h.Write(prev)
	for _, part := range [][]byte{
		[]byte(r.At.UTC().Format(time.RFC3339Nano)),
		[]byte(uuidStr(r.UserID)), []byte(uuidStr(r.CompanyID)),
		[]byte(r.Action), []byte(r.ObjectType), []byte(r.ObjectID),
		r.Before, r.After, []byte(r.IP),
	} {
		h.Write(sep)
		h.Write(part)
	}
	return h.Sum(nil)
}

// canonical menghasilkan JSON dengan urutan kunci tetap, sama persis saat ditulis maupun
// saat dibaca ulang dari JSONB, supaya hash bisa dihitung ulang.
func canonical(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	raw, ok := v.(json.RawMessage)
	if !ok {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			return nil, err
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, err
	}
	return json.Marshal(x)
}

func rawOrNil(b []byte) any {
	if b == nil {
		return nil
	}
	return json.RawMessage(b)
}

func uuidStr(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
