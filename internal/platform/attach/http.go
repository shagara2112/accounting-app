package attach

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"lajur/internal/platform/api"
	"lajur/internal/platform/auth"
	"lajur/internal/platform/httpx"
	"lajur/internal/platform/license"
	"lajur/internal/platform/rbac"
)

// Routes: izin rute cukup "anggota PT" (lihat profil PT); izin sebenarnya diperiksa per
// jenis dokumen di service.
func Routes(s *Service) []api.Route {
	h := handlers{s: s}
	download := api.Route{Method: http.MethodGet, Path: "/api/v1/attachments/{id}", Access: api.Permission, Perm: rbac.CompanyView, License: license.ModeRead, Handle: h.download, Stream: true}
	return []api.Route{
		{Method: http.MethodGet, Path: "/api/v1/attachments", Access: api.Permission, Perm: rbac.CompanyView, License: license.ModeRead, Handle: h.list},
		{Method: http.MethodPost, Path: "/api/v1/attachments", Access: api.Permission, Perm: rbac.CompanyView, License: license.ModeWrite, Handle: h.upload},
		{Method: http.MethodDelete, Path: "/api/v1/attachments/{id}", Access: api.Permission, Perm: rbac.CompanyView, License: license.ModeWrite, Handle: h.delete},
		download,
	}
}

type handlers struct{ s *Service }

func (h handlers) list(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.URL.Query().Get("objectId"))
	if err != nil {
		return httpx.BadRequest("attachment.object", "objectId tidak valid.")
	}
	items, err := h.s.List(r.Context(), api.CompanyID(r), r.URL.Query().Get("objectType"), id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// upload menerima multipart/form-data: objectType, objectId, dan file.
func (h handlers) upload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxSize+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		return httpx.BadRequest("attachment.form", "Kirim lampiran sebagai multipart/form-data.")
	}
	var objectType string
	var objectID uuid.UUID
	for {
		part, err := mr.NextPart()
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return httpx.Unprocessable("attachment.too_large", "Ukuran lampiran maksimal 10 MB.")
			}
			return httpx.BadRequest("attachment.form", "Berkas lampiran tidak ada di formulir.")
		}
		switch part.FormName() {
		case "objectType":
			buf := make([]byte, 64)
			n, _ := part.Read(buf)
			objectType = string(buf[:n])
		case "objectId":
			buf := make([]byte, 64)
			n, _ := part.Read(buf)
			if objectID, err = uuid.Parse(string(buf[:n])); err != nil {
				return httpx.BadRequest("attachment.object", "objectId tidak valid.")
			}
		case "file":
			// Field objectType dan objectId dikirim sebelum berkas.
			a, err := h.s.Upload(r.Context(), api.CompanyID(r), objectType, objectID, part.FileName(), part, auth.ActorFrom(r))
			if err != nil {
				var tooBig *http.MaxBytesError
				if errors.As(err, &tooBig) {
					return httpx.Unprocessable("attachment.too_large", "Ukuran lampiran maksimal 10 MB.")
				}
				return err
			}
			return httpx.WriteJSON(w, http.StatusCreated, a)
		}
	}
}

func (h handlers) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.s.Delete(r.Context(), api.CompanyID(r), id, auth.ActorFrom(r)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h handlers) download(w http.ResponseWriter, r *http.Request) error {
	id, err := api.PathID(r, "id")
	if err != nil {
		return err
	}
	f, a, err := h.s.Open(r.Context(), api.CompanyID(r), id)
	if err != nil {
		return err
	}
	defer f.Close()
	w.Header().Set("Content-Type", a.Mime)
	w.Header().Set("Content-Disposition", Disposition(a))
	// Berkas unggahan tidak boleh menjalankan apa pun walau dibuka langsung di browser. PDF
	// memakai CSP umum aplikasi, karena sandbox membuat penampil PDF Chrome menolak membukanya.
	if a.Mime != "application/pdf" {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, a.FileName, a.UploadedAt, f)
	return nil
}
