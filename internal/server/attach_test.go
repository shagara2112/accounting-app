package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// upload mengirim multipart seperti browser.
func (e *testEnv) upload(c *http.Client, objectType, objectID, name string, content []byte, headers ...string) result {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("objectType", objectType)
	_ = mw.WriteField("objectId", objectID)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(content)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", e.srv.URL)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return result{status: resp.StatusCode, body: out, header: resp.Header}
}

var pdf = []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n")

func TestAttachmentsFollowDocumentPermissions(t *testing.T) {
	e := newEnvWith(t, activeLicense(), func(d *Deps) { d.FilesDir = t.TempDir() })
	e.user("akuntansi@uji.test", "staf_akuntansi", e.companyA)
	e.user("manager@uji.test", "manager", e.companyA)
	e.user("pajak@uji.test", "staf_pajak", e.companyA)
	e.user("lain@uji.test", "manager", e.companyB)
	acc := e.ledgerAccounts()
	staff, mgr, tax, other := e.client(), e.client(), e.client(), e.client()
	for c, email := range map[*http.Client]string{staff: "akuntansi@uji.test", mgr: "manager@uji.test", tax: "pajak@uji.test", other: "lain@uji.test"} {
		expect(t, e.login(c, email, testPassword), http.StatusOK, "")
	}
	j := e.do(staff, http.MethodPost, "/api/v1/journals", journalBody(acc, "1000000"), "Idempotency-Key", "lampiran-uji-01")
	expect(t, j, http.StatusCreated, "")
	journal := j.body["id"].(string)

	up := e.upload(staff, "journal", journal, "nota setoran.pdf", pdf)
	expect(t, up, http.StatusCreated, "")
	id := up.body["id"].(string)
	if up.body["mime"] != "application/pdf" || up.body["fileName"] != "nota setoran.pdf" {
		t.Fatalf("lampiran = %v", up.body)
	}

	// Isi berkas menentukan jenisnya: HTML berakhiran .pdf ditolak.
	expect(t, e.upload(staff, "journal", journal, "faktur.pdf", []byte("<html><script>alert(1)</script></html>")), http.StatusUnprocessableEntity, "attachment.file_type")
	expect(t, e.upload(staff, "journal", journal, "besar.pdf", append(pdf, make([]byte, 11<<20)...)), http.StatusUnprocessableEntity, "attachment.too_large")
	// Dokumen PT lain atau id acak tidak bisa dilampiri.
	expect(t, e.upload(other, "journal", journal, "x.pdf", pdf), http.StatusNotFound, "attachment.object")

	list := e.do(mgr, http.MethodGet, "/api/v1/attachments?objectType=journal&objectId="+journal, nil)
	expect(t, list, http.StatusOK, "")
	if n := len(list.body["items"].([]any)); n != 1 {
		t.Fatalf("jumlah lampiran = %d", n)
	}
	// Staf pajak tidak punya izin lihat buku besar.
	expect(t, e.do(tax, http.MethodGet, "/api/v1/attachments?objectType=journal&objectId="+journal, nil), http.StatusForbidden, "rbac.denied")

	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/attachments/"+id, nil)
	resp, err := mgr.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, pdf) || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("unduh = %d %q %q", resp.StatusCode, resp.Header.Get("Content-Disposition"), body[:8])
	}
	// Request baru: klien dengan cookie jar menempelkan cookie ke request aslinya.
	req, _ = http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/attachments/"+id, nil)
	resp, _ = other.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("PT lain mengunduh lampiran: %d", resp.StatusCode)
	}

	// Pengunggah boleh menghapus lampirannya; Staf pajak tidak boleh.
	up2 := e.upload(mgr, "journal", journal, "rekening koran.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...))
	expect(t, up2, http.StatusCreated, "")
	expect(t, e.do(staff, http.MethodDelete, "/api/v1/attachments/"+up2.body["id"].(string), nil), http.StatusForbidden, "attachment.delete_denied")
	expect(t, e.do(staff, http.MethodDelete, "/api/v1/attachments/"+id, nil), http.StatusNoContent, "")
	list = e.do(mgr, http.MethodGet, "/api/v1/attachments?objectType=journal&objectId="+journal, nil)
	if n := len(list.body["items"].([]any)); n != 1 {
		t.Fatalf("setelah hapus, lampiran = %d", n)
	}
}
