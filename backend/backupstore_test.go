package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/minio/minio-go/v7"
)

// Needs an S3 server: KUBEPILOT_TEST_S3=127.0.0.1:9099 with KUBEPILOT_TEST_S3_KEY / KUBEPILOT_TEST_S3_SECRET (MinIO).
func TestS3Backups(t *testing.T) {
	endpoint := os.Getenv("KUBEPILOT_TEST_S3")
	if endpoint == "" {
		t.Skip("set KUBEPILOT_TEST_S3 to run against an S3 server")
	}
	ctx := context.Background()
	key, _ := age.GenerateX25519Identity()
	cfg := S3Config{Endpoint: endpoint, Bucket: "kp-test", Prefix: "kp", Insecure: true,
		AccessKey: os.Getenv("KUBEPILOT_TEST_S3_KEY"), SecretKey: os.Getenv("KUBEPILOT_TEST_S3_SECRET")}
	plain, err := newS3Store(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := plain.client.BucketExists(ctx, cfg.Bucket); !ok {
		if err := plain.client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	cfg.EncryptionKey = key.String()
	enc, err := newS3Store(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := newTestApp(t, false)
	secret := bytes.Repeat([]byte("TOKEN=super-secret-bot-token\n"), 200000) // ~5.8 MB: several parts' worth of stream

	write := func(s *s3Store, id string, data []byte) {
		a.s3 = s
		w, err := a.backupWriter(ctx, &Backup{ID: id, ServerID: "srv1", Storage: storageS3})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
		if n, err := w.Commit(); err != nil || n != int64(len(data)) {
			t.Fatalf("commit: %d %v", n, err)
		}
	}
	read := func(s *s3Store, id string) ([]byte, error) {
		a.s3 = s
		rc, _, _, err := a.openBackup(ctx, &Backup{ID: id, ServerID: "srv1", Storage: storageS3})
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	raw := func(k string) []byte {
		obj, err := plain.client.GetObject(ctx, cfg.Bucket, k, minio.GetObjectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(obj)
		return b
	}

	write(plain, "plainbackup1", secret)
	if b, err := read(plain, "plainbackup1"); err != nil || !bytes.Equal(b, secret) {
		t.Fatalf("plain round trip: %v (got %d bytes, want %d; head %q)", err, len(b), len(secret), b[:min(len(b), 60)])
	}
	write(enc, "encbackup001", secret)
	if stored := raw("kp/srv1/encbackup001.tar.gz.age"); bytes.Contains(stored, []byte("super-secret")) || !bytes.HasPrefix(stored, []byte("age-encryption.org/v1")) {
		t.Error("an encrypted backup must not contain any plaintext in the bucket")
	}
	if b, err := read(enc, "encbackup001"); err != nil || !bytes.Equal(b, secret) {
		t.Fatalf("encrypted round trip: %v", err)
	}
	if b, err := read(enc, "plainbackup1"); err != nil || !bytes.Equal(b, secret) {
		t.Errorf("turning encryption on keeps older plain backups readable: %v", err)
	}
	if _, err := read(plain, "encbackup001"); err == nil {
		t.Error("an encrypted backup can't be read without the key")
	}
	other, _ := age.GenerateX25519Identity()
	cfg.EncryptionKey = other.String()
	wrong, _ := newS3Store(cfg)
	if _, err := read(wrong, "encbackup001"); err == nil {
		t.Error("an encrypted backup can't be read with another key")
	}

	// an interrupted upload leaves nothing behind
	a.s3 = plain
	w, _ := a.backupWriter(ctx, &Backup{ID: "aborted00001", ServerID: "srv1", Storage: storageS3})
	w.Write(secret[:1<<20])
	w.Abort(errors.New("disk read error"))
	if _, err := plain.client.StatObject(ctx, cfg.Bucket, "kp/srv1/aborted00001.tar.gz", minio.StatObjectOptions{}); err == nil {
		t.Error("an aborted backup must not exist in the bucket")
	}

	for _, id := range []string{"plainbackup1", "encbackup001"} {
		a.s3 = enc
		if err := a.removeBackupData(ctx, &Backup{ID: id, ServerID: "srv1", Storage: storageS3}); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for o := range plain.client.ListObjects(ctx, cfg.Bucket, minio.ListObjectsOptions{Prefix: "kp/", Recursive: true}) {
		if o.Err == nil {
			n++
		}
	}
	if n != 0 {
		t.Errorf("deleted backups must be gone from the bucket: %d objects left", n)
	}
}

func TestS3ConfigChecks(t *testing.T) {
	if _, err := newS3Store(S3Config{Endpoint: "s3.example.com", Bucket: "b"}); err == nil {
		t.Error("credentials are required")
	}
	if _, err := newS3Store(S3Config{Endpoint: "s3.example.com", Bucket: "b", AccessKey: "a", SecretKey: "s", EncryptionKey: "not-a-key"}); err == nil {
		t.Error("an invalid encryption key must stop the panel at start, not lose backups later")
	}
	s, err := newS3Store(S3Config{Endpoint: "https://s3.example.com/", Bucket: "b", Prefix: "/kp/", AccessKey: "a", SecretKey: "s"})
	if err != nil || s.endpoint != "s3.example.com/" && s.endpoint != "s3.example.com" || s.key("srv", "id") != "kp/srv/id.tar.gz" {
		t.Errorf("endpoint and keys: %v %q %q", err, s.endpoint, s.key("srv", "id"))
	}
	if strings.Contains(s.key("srv", "id"), "//") {
		t.Error("object keys must be clean")
	}
}
