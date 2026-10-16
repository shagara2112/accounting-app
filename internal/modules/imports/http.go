package imports

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/civil"
	"lajur/internal/platform/export"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// MaxFile adalah ukuran berkas impor terbesar.
const MaxFile = 5 << 20

// Routes: setiap jenis impor punya route sendiri supaya izinnya mengikuti modul pemiliknya
// (kontak, persediaan, buku besar, penjualan, pembelian).
func Routes(svc Service) []api.Route {
	h := handlers{svc: svc}
	routes := []api.Route{
		{Method: http.MethodGet, Path: "/api/v1/imports", Access: api.Permission, Perm: rbac.CompanyView, License: license.ModeRead, Handle: h.kinds},
		{Method: http.MethodGet, Path: "/api/v1/imports/status", Access: api.Permission, Perm: rbac.LedgerView, License: license.ModeRead, Handle: h.status},
	}
	for _, k := range svc.Kinds() {
		kind := k.Kind
		routes = append(routes,
			api.Route{Method: http.MethodGet, Path: "/api/v1/imports/" + kind + "/template", Access: api.Permission, Perm: k.Perm,
				License: license.ModeRead, Handle: func(w http.ResponseWriter, r *http.Request) error { return h.template(w, r, kind) }},
			api.Route{Method: http.MethodPost, Path: "/api/v1/imports/" + kind, Access: api.Permission, Perm: k.Perm,
				License: license.ModeWrite, Idempotent: true, MaxBody: MaxFile + 64<<10,
				Handle: func(w http.ResponseWriter, r *http.Request) error { return h.run(w, r, kind) }},
		)
	}
	return routes
}

type handlers struct{ svc Service }

func (h handlers) kinds(w http.ResponseWriter, r *http.Request) error {
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": h.svc.Kinds()})
}

func (h handlers) status(w http.ResponseWriter, r *http.Request) error {
	st, err := h.svc.Status(r.Context(), api.CompanyID(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, st)
}

func (h handlers) template(w http.ResponseWriter, r *http.Request, kind string) error {
	t, err := h.svc.Template(r.Context(), api.CompanyID(r), kind)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="template-impor-`+kind+`.xlsx"`)
	return export.WriteXLSX(w, t)
}

// run menerima multipart: date (opsional, YYYY-MM-DD), dryRun ("1" untuk pratinjau), dan file.
func (h handlers) run(w http.ResponseWriter, r *http.Request, kind string) error {
	mr, err := r.MultipartReader()
	if err != nil {
		return httpx.BadRequest("import.form", "Kirim berkas sebagai multipart/form-data.")
	}
	var o Options
	var name string
	var data []byte
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return httpx.BadRequest("import.form", "Formulir impor tidak terbaca.")
		}
		switch part.FormName() {
		case "date":
			v, _ := io.ReadAll(io.LimitReader(part, 32))
			if s := strings.TrimSpace(string(v)); s != "" {
				if o.Date, err = civil.Parse(s); err != nil {
					return httpx.Unprocessable("import.date", "Tanggal tidak valid.").WithFields(map[string]string{"date": "Tanggal tidak valid."})
				}
			}
		case "dryRun":
			v, _ := io.ReadAll(io.LimitReader(part, 8))
			o.DryRun = strings.TrimSpace(string(v)) == "1"
		case "file":
			name = part.FileName()
			data, err = io.ReadAll(io.LimitReader(part, MaxFile+1))
			if err != nil {
				return httpx.BadRequest("import.form", "Berkas tidak terbaca.")
			}
			if len(data) > MaxFile {
				return httpx.Unprocessable("import.too_large", "Ukuran berkas impor maksimal 5 MB.")
			}
		}
	}
	if name == "" || len(data) == 0 {
		return httpx.Unprocessable("import.no_file", "Pilih berkas Excel atau CSV.")
	}
	res, err := h.svc.Import(r.Context(), api.CompanyID(r), kind, name, data, o, auth.ActorFrom(r))
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}
