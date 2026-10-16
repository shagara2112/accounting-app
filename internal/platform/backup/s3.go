package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3 adalah klien minimal untuk penyimpanan S3-compatible (AWS S3, MinIO, Wasabi, Cloudflare R2,
// Backblaze B2) dengan alamat path-style dan tanda tangan AWS Signature Version 4.
type S3 struct {
	Endpoint  string // mis. https://s3.ap-southeast-1.amazonaws.com
	Region    string
	Bucket    string
	Prefix    string // folder di bucket, mis. "lajur/"
	AccessKey string
	Secret    string
	Client    *http.Client
	Now       func() time.Time
}

type S3Object struct {
	Key  string
	Size int64
}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func (s *S3) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 30 * time.Minute}
}

func (s *S3) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *S3) objectURL(name string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(s.Endpoint, "/"))
	if err != nil {
		return nil, err
	}
	u.Path = "/" + s.Bucket
	if name != "" {
		u.Path += "/" + strings.TrimLeft(s.Prefix+name, "/")
	}
	return u, nil
}

// Put mengunggah satu objek (maksimal 5 GB per objek di S3).
func (s *S3) Put(ctx context.Context, name string, body io.Reader, size int64, sha256Hex string) error {
	u, err := s.objectURL(name)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	return s.do(req, sha256Hex, nil)
}

func (s *S3) Delete(ctx context.Context, name string) error {
	u, err := s.objectURL(name)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	return s.do(req, emptySHA256, nil)
}

// List mengembalikan objek di bawah Prefix (nama tanpa prefix).
func (s *S3) List(ctx context.Context) ([]S3Object, error) {
	var out []S3Object
	token := ""
	for {
		u, err := s.objectURL("")
		if err != nil {
			return nil, err
		}
		q := url.Values{"list-type": {"2"}, "prefix": {s.Prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		var res struct {
			Contents []struct {
				Key  string `xml:"Key"`
				Size int64  `xml:"Size"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}
		if err := s.do(req, emptySHA256, &res); err != nil {
			return nil, err
		}
		for _, c := range res.Contents {
			out = append(out, S3Object{Key: strings.TrimPrefix(c.Key, s.Prefix), Size: c.Size})
		}
		if !res.IsTruncated {
			return out, nil
		}
		token = res.NextContinuationToken
	}
}

func (s *S3) do(req *http.Request, payloadHash string, out any) error {
	s.sign(req, payloadHash, s.now())
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("S3 tidak bisa dihubungi: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = xml.Unmarshal(raw, &e)
		if e.Code == "" {
			e.Code = resp.Status
		}
		return fmt.Errorf("S3 menolak %s: %s %s", req.Method, e.Code, e.Message)
	}
	if out != nil {
		return xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// sign menambahkan header Authorization AWS SigV4. Semua header yang sudah ada di request
// (selain yang diatur transport) ikut ditandatangani bersama host, tanggal, dan hash isi.
func (s *S3) sign(req *http.Request, payloadHash string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	day := amzDate[:8]
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.TrimSpace(strings.Join(v, ","))
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method, uriEncode(req.URL.Path, false), canonicalQuery(req.URL.Query()),
		canonHeaders.String(), signed, payloadHash,
	}, "\n")
	scope := day + "/" + s.Region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])

	key := hmacSHA256([]byte("AWS4"+s.Secret), day)
	key = hmacSHA256(key, s.Region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string{}, q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// uriEncode mengikuti aturan SigV4: hanya A–Z a–z 0–9 - _ . ~ yang tidak dienkode;
// garis miring dipertahankan di path.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
