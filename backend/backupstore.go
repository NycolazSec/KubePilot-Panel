package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Where backup archives live: the panel's volume ("local") or an S3-compatible bucket ("s3": AWS, Backblaze B2,
// OVH, Scaleway, Cloudflare R2, MinIO…). Each backup remembers its storage, so switching to S3 keeps the older
// local backups usable. On S3, archives can be encrypted before they leave the panel (age, X25519).

const (
	storageLocal = "local"
	storageS3    = "s3"
	s3PartSize   = 16 << 20 // 16 MiB parts: bounded memory, up to 160 GB per backup
)

type S3Config struct {
	Endpoint, Region, Bucket, Prefix string
	AccessKey, SecretKey             string
	Insecure                         bool
	EncryptionKey                    string // age secret key (AGE-SECRET-KEY-1…), optional
}

type s3Store struct {
	client    *minio.Client
	bucket    string
	prefix    string
	identity  *age.X25519Identity // nil: not encrypted
	recipient *age.X25519Recipient
	endpoint  string
}

func newS3Store(c S3Config) (*s3Store, error) {
	if c.Endpoint == "" || c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		return nil, internalErr("s3: endpoint, bucket, access key and secret key are required")
	}
	endpoint := strings.TrimPrefix(strings.TrimPrefix(c.Endpoint, "https://"), "http://")
	client, err := minio.New(strings.TrimSuffix(endpoint, "/"), &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: !c.Insecure, Region: c.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}
	s := &s3Store{client: client, bucket: c.Bucket, prefix: strings.Trim(c.Prefix, "/"), endpoint: endpoint}
	if k := strings.TrimSpace(c.EncryptionKey); k != "" {
		id, err := age.ParseX25519Identity(k)
		if err != nil {
			return nil, internalErr("s3: the encryption key must be an age secret key (AGE-SECRET-KEY-1…)")
		}
		s.identity, s.recipient = id, id.Recipient()
	}
	return s, nil
}

func (s *s3Store) key(serverID, id string) string {
	name := id + ".tar.gz"
	if s.identity != nil {
		name += ".age"
	}
	return path.Join(s.prefix, serverID, name)
}

// keys: an encrypted store still reads the plain archives written before encryption was turned on, and back.
func (s *s3Store) keys(serverID, id string) []string {
	base := path.Join(s.prefix, serverID, id+".tar.gz")
	if s.identity != nil {
		return []string{base + ".age", base}
	}
	return []string{base, base + ".age"}
}

// check verifies the bucket is reachable with these credentials.
func (s *s3Store) check(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket %q not found", s.bucket)
	}
	return nil
}

// ---------- Writing

// backupWriter receives the archive; Commit makes it final, Abort throws it away.
type backupWriter interface {
	io.Writer
	Commit() (int64, error)
	Abort(error)
}

type localWriter struct {
	f    *os.File
	path string
	n    int64
}

func (w *localWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	w.n += int64(n)
	return n, err
}

func (w *localWriter) Commit() (int64, error) { return w.n, w.f.Close() }

func (w *localWriter) Abort(error) {
	w.f.Close()
	_ = os.Remove(w.path)
}

// s3Writer streams the archive to a multipart upload (and through age when encryption is on).
type s3Writer struct {
	pw   *io.PipeWriter
	enc  io.WriteCloser // age writer, or nil
	done chan error
	n    int64
}

func (w *s3Writer) Write(p []byte) (int, error) {
	var n int
	var err error
	if w.enc != nil {
		n, err = w.enc.Write(p)
	} else {
		n, err = w.pw.Write(p)
	}
	w.n += int64(n)
	return n, err
}

func (w *s3Writer) Commit() (int64, error) {
	if w.enc != nil {
		if err := w.enc.Close(); err != nil {
			w.pw.CloseWithError(err)
			<-w.done
			return 0, err
		}
	}
	w.pw.Close()
	return w.n, <-w.done
}

func (w *s3Writer) Abort(err error) {
	w.pw.CloseWithError(err) // the multipart upload is aborted: nothing is left in the bucket
	<-w.done
}

func (s *s3Store) create(ctx context.Context, serverID, id string) (backupWriter, error) {
	pr, pw := io.Pipe()
	w := &s3Writer{pw: pw, done: make(chan error, 1)}
	key := s.key(serverID, id)
	go func() {
		_, err := s.client.PutObject(ctx, s.bucket, key, pr, -1, minio.PutObjectOptions{
			ContentType: "application/octet-stream", PartSize: s3PartSize, NumThreads: 1,
			// No aws-chunked streaming signature (some S3-compatible servers store its framing in the object):
			// each part is checked by the server through its Content-MD5 instead, and TLS protects the transfer.
			DisableContentSha256: true, SendContentMd5: true,
			UserMetadata: map[string]string{"kubepilot-server": serverID},
		})
		pr.CloseWithError(err)
		w.done <- err
	}()
	if s.recipient != nil {
		enc, err := age.Encrypt(pw, s.recipient)
		if err != nil {
			w.Abort(err)
			return nil, err
		}
		w.enc = enc
	}
	return w, nil
}

func (a *App) backupStorage() string {
	if a.s3 != nil {
		return storageS3
	}
	return storageLocal
}

func (a *App) backupWriter(ctx context.Context, b *Backup) (backupWriter, error) {
	if b.Storage == storageS3 {
		if a.s3 == nil {
			return nil, errS3Off()
		}
		return a.s3.create(ctx, b.ServerID, b.ID)
	}
	p := a.backupPath(b.ServerID, b.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	return &localWriter{f: f, path: p}, nil
}

func errS3Off() error {
	return errCode(409, "This backup is stored on S3, which is no longer configured on this panel")
}

// ---------- Reading

// openBackup returns the plain archive (decrypted) and, when known, its size, modification time and a seeker
// for HTTP range requests (plain archives only).
func (a *App) openBackup(ctx context.Context, b *Backup) (io.ReadCloser, io.ReadSeeker, time.Time, error) {
	if b.Storage != storageS3 {
		f, err := os.Open(a.backupPath(b.ServerID, b.ID))
		if err != nil {
			return nil, nil, time.Time{}, ErrNotFound
		}
		st, _ := f.Stat()
		return f, f, st.ModTime(), nil
	}
	if a.s3 == nil {
		return nil, nil, time.Time{}, errS3Off()
	}
	s := a.s3
	for _, key := range s.keys(b.ServerID, b.ID) {
		obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
		if err != nil {
			continue
		}
		st, err := obj.Stat()
		if err != nil {
			obj.Close()
			continue
		}
		if !strings.HasSuffix(key, ".age") {
			return obj, obj, st.LastModified, nil
		}
		if s.identity == nil {
			obj.Close()
			return nil, nil, time.Time{}, errCode(409, "This backup is encrypted and the panel no longer has its key")
		}
		r, err := age.Decrypt(obj, s.identity)
		if err != nil {
			obj.Close()
			return nil, nil, time.Time{}, errCode(409, "This backup can't be decrypted with the panel's key")
		}
		return struct {
			io.Reader
			io.Closer
		}{r, obj}, nil, st.LastModified, nil
	}
	return nil, nil, time.Time{}, ErrNotFound
}

func (a *App) removeBackupData(ctx context.Context, b *Backup) error {
	if b.Storage != storageS3 {
		err := os.Remove(a.backupPath(b.ServerID, b.ID))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if a.s3 == nil {
		return errS3Off()
	}
	for _, key := range a.s3.keys(b.ServerID, b.ID) {
		if err := a.s3.client.RemoveObject(ctx, a.s3.bucket, key, minio.RemoveObjectOptions{}); err != nil {
			var er minio.ErrorResponse
			if errors.As(err, &er) && er.Code == "NoSuchKey" {
				continue
			}
			return err
		}
	}
	return nil
}

// GET /api/admin/backup-storage: where new backups go (never the credentials).
func (a *App) handleBackupStorage(w http.ResponseWriter, r *http.Request, u *User) {
	if a.s3 == nil {
		writeJSON(w, 200, map[string]any{"storage": storageLocal})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status := "ok"
	if err := a.s3.check(ctx); err != nil {
		log.Printf("s3: %v", err)
		status = "unreachable"
	}
	writeJSON(w, 200, map[string]any{"storage": storageS3, "endpoint": a.s3.endpoint, "bucket": a.s3.bucket,
		"prefix": a.s3.prefix, "encrypted": a.s3.identity != nil, "status": status})
}
