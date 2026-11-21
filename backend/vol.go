package main

import (
	"io"
	"io/fs"
	"os"
	"time"
)

// ---------- A server's volume, wherever it lives
//
// A volume on the panel's own node is opened directly (os.Root). A volume on another node of the cluster
// is served by that node's agent (see nodeagent.go) and reached over SSH: the panel code sees the same
// operations in both cases, with the same guarantee that no path ever leaves the volume.

// Vol is the subset of *os.Root the panel uses.
type Vol interface {
	Open(name string) (File, error)
	OpenFile(name string, flag int, perm fs.FileMode) (File, error)
	Mkdir(name string, perm fs.FileMode) error
	MkdirAll(name string, perm fs.FileMode) error
	Remove(name string) error
	RemoveAll(name string) error
	Rename(oldname, newname string) error
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	Lchown(name string, uid, gid int) error
	Chtimes(name string, atime, mtime time.Time) error
	Readlink(name string) (string, error)
	FS() fs.FS
	Close() error
}

// File is the subset of *os.File the panel uses.
type File interface {
	io.Reader
	io.Writer
	io.Seeker
	io.ReaderAt
	io.WriterAt
	io.Closer
	Stat() (fs.FileInfo, error)
	ReadDir(n int) ([]fs.DirEntry, error)
	Truncate(size int64) error
}

// localVol is a volume on this node.
type localVol struct{ r *os.Root }

func openLocalVol(dir string) (Vol, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return localVol{r}, nil
}

func fileOrNil(f *os.File, err error) (File, error) {
	if err != nil {
		return nil, err // never a typed nil in the interface
	}
	return f, nil
}

func (v localVol) Open(name string) (File, error) { return fileOrNil(v.r.Open(name)) }
func (v localVol) OpenFile(name string, flag int, perm fs.FileMode) (File, error) {
	return fileOrNil(v.r.OpenFile(name, flag, perm))
}
func (v localVol) Mkdir(name string, perm fs.FileMode) error    { return v.r.Mkdir(name, perm) }
func (v localVol) MkdirAll(name string, perm fs.FileMode) error { return v.r.MkdirAll(name, perm) }
func (v localVol) Remove(name string) error                     { return v.r.Remove(name) }
func (v localVol) RemoveAll(name string) error                  { return v.r.RemoveAll(name) }
func (v localVol) Rename(o, n string) error                     { return v.r.Rename(o, n) }
func (v localVol) Stat(name string) (fs.FileInfo, error)        { return v.r.Stat(name) }
func (v localVol) Lstat(name string) (fs.FileInfo, error)       { return v.r.Lstat(name) }
func (v localVol) Lchown(name string, uid, gid int) error       { return v.r.Lchown(name, uid, gid) }
func (v localVol) Chtimes(name string, a, m time.Time) error    { return v.r.Chtimes(name, a, m) }
func (v localVol) Readlink(name string) (string, error)         { return v.r.Readlink(name) }
func (v localVol) FS() fs.FS                                    { return v.r.FS() }
func (v localVol) Close() error                                 { return v.r.Close() }
