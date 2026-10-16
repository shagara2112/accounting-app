package mail

import (
	"bufio"
	"bytes"
	"context"
	b64 "encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"lajur/internal/platform/dbtest"
	"lajur/internal/platform/secret"
)

func TestComposeRoundTrip(t *testing.T) {
	srv := Server{From: "faktur@arta.test", FromName: "PT Arta Jasa Teknik"}
	pdf := bytes.Repeat([]byte("%PDF-1.4 isi faktur "), 20)
	raw, err := Compose(srv, Message{
		To: []string{"budi@pelanggan.test"}, Cc: []string{"keuangan@pelanggan.test"},
		Subject: "Faktur FJ/2026/10/0001 — PT Arta", Text: "Yth. Bapak Budi,\nTerlampir faktur Rp 1.000.000.\nSalam.",
		AttachmentName: "FJ-2026-10-0001.pdf", AttachmentMime: "application/pdf", Attachment: pdf,
	}, time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if subject != "Faktur FJ/2026/10/0001 — PT Arta" || msg.Header.Get("Cc") != "keuangan@pelanggan.test" {
		t.Fatalf("header = %v / %q", msg.Header, subject)
	}
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	mr := multipart.NewReader(msg.Body, params["boundary"])
	text, _ := mr.NextPart()
	body, _ := io.ReadAll(text) // quoted-printable diterjemahkan otomatis
	if !strings.Contains(string(body), "Terlampir faktur Rp 1.000.000.") {
		t.Fatalf("isi = %q", body)
	}
	att, _ := mr.NextPart()
	got, _ := io.ReadAll(att)
	if att.FileName() != "FJ-2026-10-0001.pdf" {
		t.Fatalf("lampiran = %q", att.FileName())
	}
	_ = got // isi base64 diperiksa lewat server SMTP tiruan di bawah
}

// fakeSMTP menerima satu email tanpa TLS dan menyimpan isinya.
func fakeSMTP(t *testing.T) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		w := func(s string) { conn.Write([]byte(s + "\r\n")) }
		w("220 uji ESMTP")
		var data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				w("250 uji")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				w("250 OK")
			case cmd == "DATA":
				w("354 lanjut")
				for {
					l, _ := r.ReadString('\n')
					if l == ".\r\n" {
						break
					}
					data.WriteString(l)
				}
				w("250 diterima")
				got <- data.String()
			case cmd == "QUIT":
				w("221 bye")
				return
			default:
				w("502 tidak dikenal")
			}
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), got
}

func TestSendToSMTPServer(t *testing.T) {
	addr, got := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	var p int
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	pdf := []byte("%PDF-1.4 faktur uji")
	err := Send(context.Background(), Server{Host: host, Port: p, Security: "none", From: "faktur@arta.test", FromName: "PT Arta"},
		Message{To: []string{"budi@pelanggan.test"}, Subject: "Uji", Text: "Halo", AttachmentName: "f.pdf", AttachmentMime: "application/pdf", Attachment: pdf}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw := <-got
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	mr := multipart.NewReader(msg.Body, params["boundary"])
	_, _ = mr.NextPart()
	att, _ := mr.NextPart()
	b64, _ := io.ReadAll(att)
	dec, _ := io.ReadAll(base64Reader(b64))
	if !bytes.Equal(dec, pdf) {
		t.Fatalf("lampiran rusak: %q", dec)
	}
}

func base64Reader(b []byte) io.Reader {
	return base64NewDecoder(bytes.NewReader(bytes.ReplaceAll(b, []byte("\r\n"), nil)))
}

func TestOutboxDedupeRetryAndSettings(t *testing.T) {
	pool := dbtest.Pool(t)
	box, _ := secret.New([]byte("kunci-uji-lajur-tiga-puluh-dua-b"))
	s := New(pool, box, nil)
	clk := time.Now()
	s.Now = func() time.Time { return clk }
	ctx := context.Background()

	set, _ := s.Settings(ctx)
	set.Enabled, set.Host, set.Port, set.Security, set.From = true, "smtp.contoh.test", 587, "starttls", "bukan email"
	if _, err := s.SaveSettings(ctx, set); err == nil {
		t.Fatal("alamat pengirim tidak valid harus ditolak")
	}
	set.From, set.Username, set.Password = "faktur@arta.test", "faktur@arta.test", "sandi-smtp-rahasia"
	saved, err := s.SaveSettings(ctx, set)
	if err != nil || !saved.HasPassword || saved.Password != "" {
		t.Fatalf("simpan = %+v, %v", saved, err)
	}
	var stored []byte
	_ = pool.QueryRow(ctx, "SELECT password FROM platform.mail_settings").Scan(&stored)
	if bytes.Contains(stored, []byte("sandi-smtp-rahasia")) {
		t.Fatal("password SMTP tersimpan tanpa enkripsi")
	}

	q := Queued{Kind: "backup_alert", DedupeKey: "backup:2026-10-14", Message: Message{To: []string{"admin@arta.test"}, Subject: "Backup gagal", Text: "x"}}
	if ok, err := s.Enqueue(ctx, q); !ok || err != nil {
		t.Fatalf("antre pertama = %v %v", ok, err)
	}
	if ok, _ := s.Enqueue(ctx, q); ok {
		t.Fatal("peringatan yang sama tidak boleh diantrekan dua kali")
	}
	if _, err := s.Enqueue(ctx, Queued{Kind: "invoice", Message: Message{To: []string{"bukan alamat"}}}); err == nil {
		t.Fatal("alamat salah harus ditolak")
	}

	var sentTo []string
	fail := true
	s.send = func(_ context.Context, srv Server, m Message, _ time.Time) error {
		if srv.Password != "sandi-smtp-rahasia" {
			t.Errorf("password terbuka = %q", srv.Password)
		}
		if fail {
			return errors.New("server sibuk")
		}
		sentTo = append(sentTo, m.To...)
		return nil
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	items, _ := s.Outbox(ctx)
	if items[0].Status != "queued" || items[0].Attempts != 1 || items[0].Error == nil {
		t.Fatalf("setelah gagal = %+v", items[0])
	}
	fail = false
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sentTo) != 0 {
		t.Fatal("percobaan ulang harus menunggu jedanya")
	}
	clk = clk.Add(2 * time.Minute)
	_, _ = pool.Exec(ctx, "UPDATE platform.mail_outbox SET next_attempt_at = now() - interval '1 second'")
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	items, _ = s.Outbox(ctx)
	if len(sentTo) != 1 || items[0].Status != "sent" {
		t.Fatalf("setelah dicoba ulang = %+v, terkirim ke %v", items[0], sentTo)
	}
}

var base64NewDecoder = func(r io.Reader) io.Reader { return b64.NewDecoder(b64.StdEncoding, r) }
