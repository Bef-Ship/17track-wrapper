package objstore

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeKey(t *testing.T) {
	got, err := NormalizeKey(" public/avatars/x.png ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "public/avatars/x.png" {
		t.Fatalf("got %q", got)
	}
	if _, err := NormalizeKey("../etc/passwd"); err == nil {
		t.Fatal("expected error")
	}
}

func TestIssueUploadSession(t *testing.T) {
	c, err := New(Config{
		Endpoint:       "http://obj-gateway:8080/api/v1",
		PublicEndpoint: "/api/obj/api/v1",
		JWTSecret:      "test-secret-at-least-32-characters!!",
		Bucket:         "tracking-local",
		PublicBase:     "http://127.0.0.1:9000/tracking-local",
		CDNBase:        "http://cdn.example/tracking-local",
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := c.IssueUploadSession("public/avatars/t/u/x.png", "image/png", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if sess.GatewayEndpoint != "/api/obj/api/v1" || sess.Bucket != "tracking-local" {
		t.Fatalf("bad session: %+v", sess)
	}
	if sess.Token == "" || !strings.HasPrefix(sess.CDNURL, "http://cdn.example/") {
		t.Fatalf("token/cdn: %+v", sess)
	}
	if !c.IsOwnedPublicURL(sess.CDNURL) || !c.IsOwnedPublicURL(sess.PublicURL) {
		t.Fatal("expected owned urls")
	}
}

func TestObjectKeyFromURL(t *testing.T) {
	marker := "public/avatars/t/u/"
	key, ok := ObjectKeyFromURL(
		"https://static-dev.example.com/public/avatars/t/u/233d2bd5.jpg",
		marker,
	)
	if !ok || key != "public/avatars/t/u/233d2bd5.jpg" {
		t.Fatalf("got %q ok=%v", key, ok)
	}
	key, ok = ObjectKeyFromURL(
		"http://127.0.0.1/cdn/tracking-local/public/avatars/t/u/x.png",
		marker,
	)
	if !ok || key != "public/avatars/t/u/x.png" {
		t.Fatalf("cdn path prefix: got %q ok=%v", key, ok)
	}
	if _, ok := ObjectKeyFromURL("https://evil.example/public/avatars/other/u/x.png", marker); ok {
		t.Fatal("expected reject other tenant prefix")
	}
	if _, ok := ObjectKeyFromURL("not-a-url", marker); ok {
		t.Fatal("expected reject")
	}
}
