package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	maxEditSize   = 8 << 20
	maxUploadSize = 2 << 30
)

// openRoot opens the server's data directory as an os.Root: every path below is
// resolved inside it and can never escape, even through symlinks planted by the game.
func (a *App) openRoot(ctx context.Context, s *Server) (*os.Root, int, error) {
	dir, err := a.kube.DataDir(ctx, s.ID)
	if err != nil {
		return nil, 0, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, 0, err
	}
	uid := 1000
	if t, err := a.db.Template(s.TemplateID); err == nil {
		uid = int(t.RunAsUser)
	}
	return root, uid, nil
}

// rel converts a user path ("/plugins/a.yml") to a root-relative path ("plugins/a.yml").
func rel(p string) string {
	c := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(p, "\\", "/")), "/")
	if c == "" {
		return "."
	}
	return c
}

type FileEntry struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`
	Modified int64  `json:"modified"`
	Symlink  bool   `json:"symlink"`
}

func (a *App) withRoot(w http.ResponseWriter, r *http.Request, c *Ctx, fn func(root *os.Root, uid int) error) {
	root, uid, err := a.openRoot(r.Context(), c.Server)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer root.Close()
	if err := fn(root, uid); err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			switch {
			case errors.Is(err, fs.ErrNotExist):
				err = errCode(404, "Fichier ou dossier introuvable")
			case errors.Is(err, fs.ErrExist):
				err = errCode(409, "Existe déjà")
			default:
				err = errCode(400, "Opération refusée : "+pe.Err.Error())
			}
		}
		writeErr(w, err)
	}
}

func (a *App) handleFileList(w http.ResponseWriter, r *http.Request, c *Ctx) {
	a.withRoot(w, r, c, func(root *os.Root, _ int) error {
		f, err := root.Open(rel(r.URL.Query().Get("path")))
		if err != nil {
			return err
		}
		defer f.Close()
		ents, err := f.ReadDir(-1)
		if err != nil {
			return errCode(400, "Ce n'est pas un dossier")
		}
		out := make([]FileEntry, 0, len(ents))
		for _, e := range ents {
			info, err := e.Info()
			if err != nil {
				continue
			}
			fe := FileEntry{Name: e.Name(), Dir: e.IsDir(), Size: info.Size(), Mode: info.Mode().Perm().String(), Modified: info.ModTime().Unix(), Symlink: info.Mode()&fs.ModeSymlink != 0}
			if fe.Dir {
				fe.Size = 0
			}
			out = append(out, fe)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Dir != out[j].Dir {
				return out[i].Dir
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
		writeJSON(w, 200, out)
		return nil
	})
}

func (a *App) handleFileRead(w http.ResponseWriter, r *http.Request, c *Ctx) {
	a.withRoot(w, r, c, func(root *os.Root, _ int) error {
		f, err := root.Open(rel(r.URL.Query().Get("path")))
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if st.IsDir() {
			return errCode(400, "C'est un dossier")
		}
		if st.Size() > maxEditSize {
			return errCode(413, "Fichier trop gros pour l'éditeur (8 Mo max) : télécharge-le")
		}
		b, err := io.ReadAll(io.LimitReader(f, maxEditSize))
		if err != nil {
			return err
		}
		head := b
		if len(head) > 8000 {
			head = head[:8000]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return errCode(415, "Fichier binaire : impossible de l'éditer")
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(b)
		return nil
	})
}

func chownNew(root *os.Root, p string, uid int) { _ = root.Lchown(p, uid, uid) }

func (a *App) handleFileWrite(w http.ResponseWriter, r *http.Request, c *Ctx) {
	p := rel(r.URL.Query().Get("path"))
	if p == "." {
		writeErr(w, errCode(400, "Chemin invalide"))
		return
	}
	a.withRoot(w, r, c, func(root *os.Root, uid int) error {
		f, err := root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(r.Body, maxEditSize+1))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		chownNew(root, p, uid)
		go a.refreshDisk(a.ctx, c.Server.ID)
		c.log(a, "server.file.write", "/"+p)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
}

func (a *App) handleMkdir(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Path string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	p := rel(in.Path)
	a.withRoot(w, r, c, func(root *os.Root, uid int) error {
		if err := root.Mkdir(p, 0o755); err != nil {
			return err
		}
		chownNew(root, p, uid)
		c.log(a, "server.file.mkdir", "/"+p)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
}

func (a *App) handleRename(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ From, To string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	from, to := rel(in.From), rel(in.To)
	if from == "." || to == "." {
		writeErr(w, errCode(400, "Chemin invalide"))
		return
	}
	a.withRoot(w, r, c, func(root *os.Root, _ int) error {
		if _, err := root.Lstat(to); err == nil {
			return errCode(409, "La destination existe déjà")
		}
		if err := root.Rename(from, to); err != nil {
			return err
		}
		c.log(a, "server.file.rename", "/"+from+" → /"+to)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
}

func (a *App) handleDelete(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Paths []string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	a.withRoot(w, r, c, func(root *os.Root, _ int) error {
		for _, p := range in.Paths {
			rp := rel(p)
			if rp == "." {
				return errCode(400, "Impossible de supprimer la racine")
			}
			if err := root.RemoveAll(rp); err != nil {
				return err
			}
		}
		go a.refreshDisk(a.ctx, c.Server.ID)
		c.log(a, "server.file.delete", strings.Join(in.Paths, ", "))
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
}

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request, c *Ctx) {
	dir := rel(r.URL.Query().Get("path"))
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, errCode(400, "Upload invalide"))
		return
	}
	a.withRoot(w, r, c, func(root *os.Root, uid int) error {
		names := []string{}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return errCode(400, "Upload interrompu ou trop gros (2 Go max)")
			}
			name := part.FileName() // already reduced to a base name by mime/multipart
			if name == "" || name == "." || name == ".." {
				part.Close()
				continue
			}
			target := path.Join(dir, name)
			f, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
			if err != nil {
				part.Close()
				return err
			}
			_, err = io.Copy(f, part)
			f.Close()
			part.Close()
			if err != nil {
				_ = root.Remove(target)
				return errCode(400, "Upload interrompu ou trop gros (2 Go max)")
			}
			chownNew(root, target, uid)
			names = append(names, name)
		}
		go a.refreshDisk(a.ctx, c.Server.ID)
		c.log(a, "server.file.upload", "/"+dir+" : "+strings.Join(names, ", "))
		writeJSON(w, 200, map[string]any{"ok": true, "files": names})
		return nil
	})
}

func contentDisposition(name string) string {
	safe := strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' || r > 126 {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"`, safe)
}

func (a *App) handleDownload(w http.ResponseWriter, r *http.Request, c *Ctx) {
	p := rel(r.URL.Query().Get("path"))
	a.withRoot(w, r, c, func(root *os.Root, _ int) error {
		st, err := root.Stat(p)
		if err != nil {
			return err
		}
		if !st.IsDir() {
			f, err := root.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			w.Header().Set("Content-Disposition", contentDisposition(path.Base(p)))
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, path.Base(p), st.ModTime(), f)
			return nil
		}
		name := path.Base(p)
		if p == "." {
			name = c.Server.Name
		}
		w.Header().Set("Content-Disposition", contentDisposition(name+".zip"))
		w.Header().Set("Content-Type", "application/zip")
		zw := zip.NewWriter(w)
		_ = writeZip(zw, root, p)
		_ = zw.Close()
		c.log(a, "server.file.download", "/"+p+" (zip)")
		return nil
	})
}

func writeZip(zw *zip.Writer, root *os.Root, base string) error {
	fsys := root.FS()
	return fs.WalkDir(fsys, base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == base || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		name := strings.TrimPrefix(p, base+"/")
		if base == "." {
			name = p
		}
		hdr, _ := zip.FileInfoHeader(info)
		hdr.Name = name
		if d.IsDir() {
			hdr.Name += "/"
			_, err = zw.CreateHeader(hdr)
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		hdr.Method = zip.Deflate
		zf, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := fsys.Open(p)
		if err != nil {
			return nil
		}
		_, err = io.Copy(zf, f)
		f.Close()
		return err
	})
}

// writeTarGz archives `paths` (relative to root) into w. Symlinks are kept as links, never followed.
func writeTarGz(w io.Writer, root *os.Root, paths []string) error {
	gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	fsys := root.FS()
	for _, base := range paths {
		err := fs.WalkDir(fsys, base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if p == "." {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			link := ""
			if info.Mode()&fs.ModeSymlink != 0 {
				link, _ = root.Readlink(p)
			} else if !info.Mode().IsRegular() && !info.IsDir() {
				return nil
			}
			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return nil
			}
			hdr.Name = p
			if info.IsDir() {
				hdr.Name += "/"
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				f, err := fsys.Open(p)
				if err != nil {
					return err
				}
				_, err = io.CopyN(tw, f, hdr.Size)
				f.Close()
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// extractTar safely extracts a tar stream into root/dest. Links and special files are skipped.
func extractTar(r io.Reader, root *os.Root, dest string, uid int, maxBytes int64) error {
	tr := tar.NewReader(r)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errCode(400, "Archive corrompue")
		}
		target := rel(path.Join(dest, hdr.Name))
		if target == "." {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = root.MkdirAll(target, 0o755)
			chownNew(root, target, uid)
		case tar.TypeReg:
			total += hdr.Size
			if maxBytes > 0 && total > maxBytes {
				return errCode(413, "L'archive dépasse l'espace disque autorisé")
			}
			if dir := path.Dir(target); dir != "." {
				_ = root.MkdirAll(dir, 0o755)
			}
			f, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, hdr.FileInfo().Mode().Perm()|0o600)
			if err != nil {
				return err
			}
			_, err = io.CopyN(f, tr, hdr.Size)
			f.Close()
			if err != nil {
				return errCode(400, "Archive corrompue")
			}
			chownNew(root, target, uid)
			_ = root.Chtimes(target, hdr.ModTime, hdr.ModTime)
		}
	}
}

func extractZip(zr *zip.Reader, root *os.Root, dest string, uid int, maxBytes int64) error {
	var total int64
	for _, zf := range zr.File {
		target := rel(path.Join(dest, zf.Name))
		if target == "." {
			continue
		}
		mode := zf.Mode()
		if mode.IsDir() || strings.HasSuffix(zf.Name, "/") {
			_ = root.MkdirAll(target, 0o755)
			chownNew(root, target, uid)
			continue
		}
		if !mode.IsRegular() {
			continue
		}
		total += int64(zf.UncompressedSize64)
		if maxBytes > 0 && total > maxBytes {
			return errCode(413, "L'archive dépasse l'espace disque autorisé")
		}
		if dir := path.Dir(target); dir != "." {
			_ = root.MkdirAll(dir, 0o755)
		}
		src, err := zf.Open()
		if err != nil {
			return errCode(400, "Archive corrompue")
		}
		f, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			src.Close()
			return err
		}
		n, err := io.Copy(f, io.LimitReader(src, int64(zf.UncompressedSize64)+1))
		f.Close()
		src.Close()
		if err != nil || n > int64(zf.UncompressedSize64) {
			return errCode(400, "Archive corrompue")
		}
		chownNew(root, target, uid)
	}
	return nil
}

func (a *App) handleCompress(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct {
		Dir   string   `json:"dir"`
		Paths []string `json:"paths"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if len(in.Paths) == 0 {
		writeErr(w, errCode(400, "Rien à compresser"))
		return
	}
	dir := rel(in.Dir)
	a.withRoot(w, r, c, func(root *os.Root, uid int) error {
		name := "archive-" + time.Now().Format("2006-01-02_150405") + ".tar.gz"
		target := path.Join(dir, name)
		paths := []string{}
		for _, p := range in.Paths {
			if rp := rel(p); rp != "." {
				paths = append(paths, rp)
			}
		}
		// paths inside the archive are relative to the server root
		f, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		err = writeTarGz(f, root, paths)
		f.Close()
		if err != nil {
			_ = root.Remove(target)
			return err
		}
		chownNew(root, target, uid)
		c.log(a, "server.file.compress", "/"+target)
		writeJSON(w, 200, map[string]string{"name": name})
		return nil
	})
}

func (a *App) handleDecompress(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Path string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	p := rel(in.Path)
	maxBytes := int64(c.Server.DiskMB)*1024*1024 - a.diskUsed(c.Server.ID)
	if maxBytes < 1 {
		writeErr(w, errCode(413, "Plus d'espace disque disponible"))
		return
	}
	a.withRoot(w, r, c, func(root *os.Root, uid int) error {
		dest := path.Dir(p)
		lower := strings.ToLower(p)
		f, err := root.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		switch {
		case strings.HasSuffix(lower, ".zip"):
			st, err := f.Stat()
			if err != nil {
				return err
			}
			zr, err := zip.NewReader(f, st.Size())
			if err != nil {
				return errCode(400, "ZIP invalide")
			}
			err = extractZip(zr, root, dest, uid, maxBytes)
			if err != nil {
				return err
			}
		case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
			gz, err := gzip.NewReader(f)
			if err != nil {
				return errCode(400, "Archive gzip invalide")
			}
			if err := extractTar(gz, root, dest, uid, maxBytes); err != nil {
				return err
			}
		case strings.HasSuffix(lower, ".tar"):
			if err := extractTar(f, root, dest, uid, maxBytes); err != nil {
				return err
			}
		default:
			return errCode(400, "Format non supporté (.zip, .tar, .tar.gz, .tgz)")
		}
		go a.refreshDisk(a.ctx, c.Server.ID)
		c.log(a, "server.file.decompress", "/"+p)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
}

// ---------- Disk usage

func dirSize(dir string) int64 {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0
	}
	defer root.Close()
	var total int64
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func (a *App) refreshDisk(ctx context.Context, id string) {
	dir, err := a.kube.DataDir(ctx, id)
	if err != nil {
		return
	}
	size := dirSize(dir)
	a.diskMu.Lock()
	a.disk[id] = size
	a.diskMu.Unlock()
}

func (a *App) diskLoop(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		if servers, err := a.db.AllServers(); err == nil {
			for _, s := range servers {
				a.refreshDisk(ctx, s.ID)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
