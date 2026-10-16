package backup

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Contoh "GET Object" dari dokumentasi AWS Signature Version 4 untuk S3.
func TestS3SignMatchesAWSExample(t *testing.T) {
	s := &S3{Region: "us-east-1", AccessKey: "AKIAIOSFODNN7EXAMPLE", Secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	s.sign(req, emptySHA256, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization =\n%s\nseharusnya\n%s", got, want)
	}
}

func TestURIEncode(t *testing.T) {
	if got := uriEncode("/bucket/lajur bk/a+b$~.x", false); got != "/bucket/lajur%20bk/a%2Bb%24~.x" {
		t.Fatalf("path = %s", got)
	}
	if got := uriEncode("a/b", true); !strings.Contains(got, "%2F") {
		t.Fatalf("query = %s", got)
	}
}
