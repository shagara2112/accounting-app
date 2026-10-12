package audit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lajur/internal/platform/db"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/rbac"
	"lajur/internal/platform/store"
)

type item struct {
	Seq        int64           `json:"seq"`
	At         time.Time       `json:"at"`
	UserID     *uuid.UUID      `json:"userId,omitempty"`
	Action     string          `json:"action"`
	ObjectType string          `json:"objectType"`
	ObjectID   string          `json:"objectId,omitempty"`
	Before     json.RawMessage `json:"before,omitempty"`
	After      json.RawMessage `json:"after,omitempty"`
	IP         string          `json:"ip,omitempty"`
}

// ListHandler menampilkan audit trail PT aktif beserta kejadian tingkat sistem (mis. login).
func ListHandler(pool *pgxpool.Pool) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		scope, _ := rbac.ScopeFrom(r.Context())
		limit := 50
		if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
			limit = v
		}
		rows, err := store.New(db.Conn(r.Context(), pool)).ListAudit(r.Context(), store.ListAuditParams{
			CompanyID: &scope.CompanyID, RowLimit: int32(limit),
		})
		if err != nil {
			return err
		}
		out := make([]item, 0, len(rows))
		for _, a := range rows {
			out = append(out, item{
				Seq: a.Seq, At: a.At, UserID: a.UserID, Action: a.Action, ObjectType: a.ObjectType,
				ObjectID: deref(a.ObjectID), Before: a.Before, After: a.After, IP: deref(a.Ip),
			})
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

func VerifyHandler(pool *pgxpool.Pool) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		res, err := Verify(r.Context(), db.Conn(r.Context(), pool))
		if err != nil {
			return err
		}
		return httpx.WriteJSON(w, http.StatusOK, res)
	}
}
