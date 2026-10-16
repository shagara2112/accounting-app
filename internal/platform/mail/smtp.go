package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Message adalah satu email yang siap dikirim.
type Message struct {
	To, Cc         []string
	Subject        string
	Text           string
	AttachmentName string
	AttachmentMime string
	Attachment     []byte
}

// Server adalah pengaturan SMTP yang sudah dibuka (password terdekripsi).
type Server struct {
	Host     string
	Port     int
	Security string // starttls, tls, none
	Username string
	Password string
	From     string
	FromName string
}

// Compose menyusun email MIME: teks UTF-8 (quoted-printable) dan satu lampiran opsional.
func Compose(srv Server, m Message, now time.Time) ([]byte, error) {
	var buf bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	from := (&mail.Address{Name: srv.FromName, Address: srv.From}).String()
	header("From", from)
	header("To", strings.Join(m.To, ", "))
	if len(m.Cc) > 0 {
		header("Cc", strings.Join(m.Cc, ", "))
	}
	header("Subject", mime.BEncoding.Encode("UTF-8", m.Subject))
	header("Date", now.Format(time.RFC1123Z))
	host := srv.From[strings.LastIndex(srv.From, "@")+1:]
	header("Message-ID", "<"+uuid.NewString()+"@"+host+">")
	header("MIME-Version", "1.0")

	mw := multipart.NewWriter(&buf)
	header("Content-Type", `multipart/mixed; boundary="`+mw.Boundary()+`"`)
	buf.WriteString("\r\n")

	text, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=UTF-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return nil, err
	}
	qp := quotedprintable.NewWriter(text)
	if _, err := io.WriteString(qp, strings.ReplaceAll(m.Text, "\n", "\r\n")); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	if len(m.Attachment) > 0 {
		part, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType(m.AttachmentMime, map[string]string{"name": m.AttachmentName})},
			"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": m.AttachmentName})},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, err
		}
		enc := base64.StdEncoding.EncodeToString(m.Attachment)
		for len(enc) > 76 {
			io.WriteString(part, enc[:76]+"\r\n")
			enc = enc[76:]
		}
		io.WriteString(part, enc+"\r\n")
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Send mengirim satu email lewat SMTP. Mode keamanan: tls (SMTPS, port 465), starttls (587),
// atau none (relay di jaringan kantor). Login hanya dikirim lewat koneksi terenkripsi,
// kecuali ke server di mesin yang sama.
func Send(ctx context.Context, srv Server, m Message, now time.Time) error {
	raw, err := Compose(srv, m, now)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	tlsCfg := &tls.Config{ServerName: srv.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if srv.Security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("server SMTP %s tidak bisa dihubungi: %w", addr, err)
	}
	_ = conn.SetDeadline(now.Add(2 * time.Minute))
	c, err := smtp.NewClient(conn, srv.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP: %w", err)
	}
	defer c.Close()
	if srv.Security == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("server SMTP tidak mendukung STARTTLS; pilih keamanan lain")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS gagal: %w", err)
		}
	}
	if srv.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", srv.Username, srv.Password, srv.Host)); err != nil {
			return fmt.Errorf("login SMTP ditolak: %w", err)
		}
	}
	if err := c.Mail(srv.From); err != nil {
		return fmt.Errorf("pengirim ditolak: %w", err)
	}
	for _, rcpt := range append(append([]string{}, m.To...), m.Cc...) {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("penerima %s ditolak: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email ditolak server: %w", err)
	}
	return c.Quit()
}
