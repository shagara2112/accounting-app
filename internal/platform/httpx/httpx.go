// Package httpx berisi format error problem+json (RFC 9457) dan helper JSON.
package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// Handler mengembalikan error alih-alih menulis error sendiri; pembungkus route
// mengubahnya menjadi problem+json dan me-rollback transaksi.
type Handler func(w http.ResponseWriter, r *http.Request) error

// Problem adalah error yang aman ditampilkan ke pengguna, dengan kode bisnis yang stabil.
type Problem struct {
	Type   string            `json:"type"`
	Status int               `json:"status"`
	Code   string            `json:"code"`
	Title  string            `json:"title"`
	Detail string            `json:"detail,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

func (p *Problem) Error() string {
	if p.Detail != "" {
		return p.Code + ": " + p.Detail
	}
	return p.Code
}

func NewProblem(status int, code, title, detail string) *Problem {
	return &Problem{Type: "about:blank", Status: status, Code: code, Title: title, Detail: detail}
}

func BadRequest(code, detail string) *Problem {
	return NewProblem(http.StatusBadRequest, code, "Permintaan tidak valid", detail)
}

func Unauthorized(code, detail string) *Problem {
	return NewProblem(http.StatusUnauthorized, code, "Perlu masuk", detail)
}

func Forbidden(code, detail string) *Problem {
	return NewProblem(http.StatusForbidden, code, "Akses ditolak", detail)
}

func NotFound(code, detail string) *Problem {
	return NewProblem(http.StatusNotFound, code, "Tidak ditemukan", detail)
}

func Conflict(code, detail string) *Problem {
	return NewProblem(http.StatusConflict, code, "Data berubah atau sudah ada", detail)
}

// Unprocessable dipakai untuk aturan bisnis yang dilanggar, mis. jurnal tidak seimbang.
func Unprocessable(code, detail string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, code, "Tidak bisa diproses", detail)
}

// WithFields menambahkan pesan per field, mis. {"lines.2.accountId": "Akun tidak aktif."}.
func (p *Problem) WithFields(f map[string]string) *Problem {
	if len(f) > 0 {
		p.Fields = f
	}
	return p
}

// WriteProblem menulis p apa adanya.
func WriteProblem(w http.ResponseWriter, p *Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// WriteError menulis err sebagai problem+json. Error selain *Problem dicatat ke log
// dan dijawab 500 tanpa detail, supaya pesan internal tidak bocor ke pengguna.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var p *Problem
	if !errors.As(err, &p) {
		log.ErrorContext(r.Context(), "kesalahan internal", "err", err, "path", r.URL.Path)
		p = NewProblem(http.StatusInternalServerError, "internal", "Terjadi kesalahan di server", "")
	}
	WriteProblem(w, p)
}

func WriteJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// DecodeJSON membaca body JSON (maks. 1 MB) dan menolak field yang tidak dikenal.
func DecodeJSON(r *http.Request, dst any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return NewProblem(http.StatusUnsupportedMediaType, "request.content_type",
			"Format tidak didukung", "Kirim body sebagai application/json.")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return BadRequest("request.body", "Body JSON tidak bisa dibaca: "+err.Error())
	}
	return nil
}

// BufferedWriter menahan respons sampai transaksi berhasil di-commit,
// sehingga pengguna tidak pernah menerima "sukses" untuk data yang batal tersimpan.
type BufferedWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func NewBufferedWriter() *BufferedWriter { return &BufferedWriter{header: http.Header{}} }

func (b *BufferedWriter) Header() http.Header { return b.header }

func (b *BufferedWriter) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *BufferedWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// Status adalah status yang akan dikirim (204 bila handler tidak menulis apa pun).
func (b *BufferedWriter) Status() int {
	if b.status == 0 {
		return http.StatusNoContent
	}
	return b.status
}

func (b *BufferedWriter) Bytes() []byte { return b.body.Bytes() }

func (b *BufferedWriter) FlushTo(w http.ResponseWriter) {
	for k, v := range b.header {
		w.Header()[k] = v
	}
	if b.status == 0 {
		b.status = http.StatusNoContent
	}
	w.WriteHeader(b.status)
	_, _ = w.Write(b.body.Bytes())
}
