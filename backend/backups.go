package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

type Backup struct {
	ID          string `json:"id"`
	ServerID    string `json:"serverId"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Status      string `json:"status"` // pending, completed, failed
	Error       string `json:"error"`
	Locked      bool   `json:"locked"`
	CreatedAt   int64  `json:"createdAt"`
	CompletedAt int64  `json:"completedAt"`
	Storage     string `json:"storage"` // local, s3
}

func (a *App) backupPath(serverID, id string) string {
	return filepath.Join(a.cfg.DataDir, "backups", serverID, id+".tar.gz")
}

func (a *App) listBackups(serverID string) ([]*Backup, error) {
	rows, err := a.db.Query(`SELECT id, server_id, name, size, status, error, locked, created_at, completed_at, storage FROM backups WHERE server_id=? ORDER BY created_at DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Backup{}
	for rows.Next() {
		b := &Backup{}
		if err := rows.Scan(&b.ID, &b.ServerID, &b.Name, &b.Size, &b.Status, &b.Error, &b.Locked, &b.CreatedAt, &b.CompletedAt, &b.Storage); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (a *App) getBackup(serverID, id string) (*Backup, error) {
	b := &Backup{}
	err := a.db.QueryRow(`SELECT id, server_id, name, size, status, error, locked, created_at, completed_at, storage FROM backups WHERE server_id=? AND id=?`, serverID, id).
		Scan(&b.ID, &b.ServerID, &b.Name, &b.Size, &b.Status, &b.Error, &b.Locked, &b.CreatedAt, &b.CompletedAt, &b.Storage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func (a *App) deleteBackup(b *Backup) error {
	ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
	defer cancel()
	if err := a.removeBackupData(ctx, b); err != nil {
		log.Printf("backup %s: %v", b.ID, err)
		var ae *apiError
		if errors.As(err, &ae) {
			return err
		}
		return errCode(502, "The backup storage can't be reached right now")
	}
	_, _ = a.db.Exec(`DELETE FROM backups WHERE id=?`, b.ID)
	return nil
}

// createBackup starts a backup. rotate=true deletes the oldest unlocked backup when the limit is reached.
func (a *App) createBackup(s *Server, name string, rotate bool) (*Backup, error) {
	if s.BackupLimit <= 0 {
		return nil, errCode(403, "Backups are disabled for this server")
	}
	list, err := a.listBackups(s.ID)
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		if b.Status == "pending" {
			return nil, errCode(409, "A backup is already in progress")
		}
	}
	if len(list) >= s.BackupLimit {
		if !rotate {
			return nil, errCode(409, "Backup limit of %d reached: delete one first", s.BackupLimit)
		}
		var oldest *Backup
		for _, b := range list {
			if !b.Locked {
				oldest = b // list is newest first
			}
		}
		if oldest == nil {
			return nil, errCode(409, "Limit reached and all backups are locked")
		}
		if err := a.deleteBackup(oldest); err != nil {
			return nil, err
		}
	}
	if _, err := a.kube.Volume(a.ctx, s.ID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Backup " + time.Now().In(a.loc()).Format("2006-01-02 15:04")
	}
	b := &Backup{ID: randomID(12), ServerID: s.ID, Name: truncate(name, 80), Status: "pending", CreatedAt: now(), Storage: a.backupStorage()}
	if _, err := a.db.Exec(`INSERT INTO backups(id,server_id,name,status,created_at,storage) VALUES(?,?,?,?,?,?)`, b.ID, s.ID, b.Name, b.Status, b.CreatedAt, b.Storage); err != nil {
		return nil, err
	}
	go a.runBackup(s, b)
	return b, nil
}

func (a *App) runBackup(s *Server, b *Backup) {
	h := a.hub(s.ID)
	h.daemon("Backing up “%s”…", b.Name)
	fail := func(err error) {
		_, _ = a.db.Exec(`UPDATE backups SET status='failed', error=?, completed_at=? WHERE id=?`, err.Error(), now(), b.ID)
		h.daemon("Backup failed: %s", err.Error())
	}
	if t, err := a.db.Template(s.TemplateID); err == nil && t.ConsoleInput && strings.HasPrefix(t.Slug, "minecraft") && a.kube.State(s.ID).Status == "running" {
		if a.kube.SendStdin(s.ID, "save-all flush") == nil {
			time.Sleep(4 * time.Second)
		}
	}
	root, _, err := a.openRoot(a.ctx, s)
	if err != nil {
		fail(err)
		return
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		fail(err)
		return
	}
	paths := []string{}
	for _, e := range entries {
		paths = append(paths, e.Name())
	}
	ctx, cancel := context.WithTimeout(a.ctx, 6*time.Hour)
	defer cancel()
	out, err := a.backupWriter(ctx, b)
	if err != nil {
		fail(err)
		return
	}
	if err := writeTarGz(out, root, paths); err != nil {
		out.Abort(err)
		fail(err)
		return
	}
	size, err := out.Commit()
	if err != nil {
		log.Printf("backup %s: %v", b.ID, err)
		fail(errors.New(tr("en", "The backup storage can't be reached right now")))
		return
	}
	_, _ = a.db.Exec(`UPDATE backups SET status='completed', size=?, completed_at=? WHERE id=?`, size, now(), b.ID)
	h.daemon("Backup completed ✔ (%s)", bytesArg(size))
}

func (a *App) handleBackups(w http.ResponseWriter, r *http.Request, c *Ctx) {
	list, err := a.listBackups(c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"backups": list, "limit": c.Server.BackupLimit})
}

func (a *App) handleBackupCreate(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct{ Name string }
	_ = readJSON(r, &in)
	b, err := a.createBackup(c.Server, in.Name, false)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	c.log(a, "server.backup.create", b.Name)
	writeJSON(w, 200, b)
}

func (a *App) handleBackupDownload(w http.ResponseWriter, r *http.Request, c *Ctx) {
	b, err := a.getBackup(c.Server.ID, r.PathValue("bid"))
	if err != nil || b.Status != "completed" {
		writeErr(w, r, ErrNotFound)
		return
	}
	rc, seeker, mod, err := a.openBackup(r.Context(), b)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	defer rc.Close()
	c.log(a, "server.backup.download", b.Name)
	w.Header().Set("Content-Disposition", contentDisposition(fmt.Sprintf("%s-%s.tar.gz", c.Server.Name, time.Unix(b.CreatedAt, 0).Format("20060102-150405"))))
	w.Header().Set("Content-Type", "application/gzip")
	if seeker != nil {
		http.ServeContent(w, r, "", mod, seeker)
		return
	}
	_, _ = io.Copy(w, rc) // decrypted on the fly: no range requests
}

func (a *App) handleBackupRestore(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in struct {
		Truncate bool `json:"truncate"`
	}
	_ = readJSON(r, &in)
	b, err := a.getBackup(c.Server.ID, r.PathValue("bid"))
	if err != nil || b.Status != "completed" {
		writeErr(w, r, ErrNotFound)
		return
	}
	if st := a.kube.State(c.Server.ID).Status; st != "offline" {
		writeErr(w, r, errCode(409, "Stop the server before restoring a backup"))
		return
	}
	lock := a.serverLock(c.Server.ID)
	if !lock.TryLock() {
		writeErr(w, r, errCode(409, "Another action is already running on this server"))
		return
	}
	defer lock.Unlock()
	a.clearAsleep(r.Context(), c.Server) // mc-router must not start it while its files are replaced
	h := a.hub(c.Server.ID)
	h.daemon("Restoring “%s”…", b.Name)
	a.withRoot(w, r, c, func(root Vol, uid int) error {
		if in.Truncate {
			entries, err := fs.ReadDir(root.FS(), ".")
			if err != nil {
				return err
			}
			for _, e := range entries {
				if err := root.RemoveAll(e.Name()); err != nil {
					return err
				}
			}
		}
		rc, _, _, err := a.openBackup(r.Context(), b)
		if err != nil {
			return err
		}
		defer rc.Close()
		gz, err := gzip.NewReader(rc)
		if err != nil {
			return errCode(400, "Corrupted backup")
		}
		if err := extractTar(gz, root, ".", uid, 0); err != nil {
			h.daemon("Restore failed: %s", err.Error())
			return err
		}
		h.daemon("Restore completed ✔")
		c.log(a, "server.backup.restore", b.Name)
		go a.refreshDisk(a.ctx, c.Server.ID)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
}

func (a *App) handleBackupLock(w http.ResponseWriter, r *http.Request, c *Ctx) {
	b, err := a.getBackup(c.Server.ID, r.PathValue("bid"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	_, _ = a.db.Exec(`UPDATE backups SET locked=? WHERE id=?`, !b.Locked, b.ID)
	c.log(a, "server.backup.lock", fmt.Sprintf("%s → %v", b.Name, !b.Locked))
	writeJSON(w, 200, map[string]bool{"locked": !b.Locked})
}

func (a *App) handleBackupDelete(w http.ResponseWriter, r *http.Request, c *Ctx) {
	b, err := a.getBackup(c.Server.ID, r.PathValue("bid"))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if b.Locked {
		writeErr(w, r, errCode(409, "This backup is locked: unlock it first"))
		return
	}
	if b.Status == "pending" {
		writeErr(w, r, errCode(409, "Backup in progress"))
		return
	}
	if err := a.deleteBackup(b); err != nil {
		writeErr(w, r, err)
		return
	}
	c.log(a, "server.backup.delete", b.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- Schedules

type Schedule struct {
	ID         int64  `json:"id"`
	ServerID   string `json:"serverId"`
	Name       string `json:"name"`
	Cron       string `json:"cron"`
	Action     string `json:"action"` // command, start, stop, restart, backup
	Payload    string `json:"payload"`
	Enabled    bool   `json:"enabled"`
	OnlyOnline bool   `json:"onlyOnline"`
	LastRun    int64  `json:"lastRun"`
	NextRun    int64  `json:"nextRun"`
	LastResult string `json:"lastResult"`
	CreatedAt  int64  `json:"createdAt"`
}

const schedCols = `id, server_id, name, cron, action, payload, enabled, only_online, last_run, next_run, last_result, created_at`

func scanSchedules(rows *sql.Rows) ([]*Schedule, error) {
	defer rows.Close()
	out := []*Schedule{}
	for rows.Next() {
		s := &Schedule{}
		if err := rows.Scan(&s.ID, &s.ServerID, &s.Name, &s.Cron, &s.Action, &s.Payload, &s.Enabled, &s.OnlyOnline, &s.LastRun, &s.NextRun, &s.LastResult, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (a *App) loc() *time.Location {
	if l, err := time.LoadLocation(a.db.Settings().Timezone); err == nil {
		return l
	}
	return time.UTC
}

func (a *App) nextRun(expr string) (int64, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return 0, errCode(400, "Invalid cron expression (format: minute hour day month weekday)")
	}
	return sched.Next(time.Now().In(a.loc())).Unix(), nil
}

func (a *App) handleSchedules(w http.ResponseWriter, r *http.Request, c *Ctx) {
	rows, err := a.db.Query(`SELECT `+schedCols+` FROM schedules WHERE server_id=? ORDER BY id`, c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	list, err := scanSchedules(rows)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"schedules": list, "timezone": a.db.Settings().Timezone})
}

func (a *App) handleScheduleSave(w http.ResponseWriter, r *http.Request, c *Ctx) {
	var in Schedule
	if err := readJSON(r, &in); err != nil {
		writeErr(w, r, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = "Tâche"
	}
	switch in.Action {
	case "command":
		if strings.TrimSpace(in.Payload) == "" || strings.ContainsAny(in.Payload, "\r\n") {
			writeErr(w, r, errCode(400, "A command is required (single line)"))
			return
		}
	case "start", "stop", "restart", "backup":
	default:
		writeErr(w, r, errCode(400, "Invalid action"))
		return
	}
	next, err := a.nextRun(in.Cron)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if sid := r.PathValue("sid"); sid != "" {
		id, _ := strconv.ParseInt(sid, 10, 64)
		if affected(a.db.Exec(`UPDATE schedules SET name=?, cron=?, action=?, payload=?, enabled=?, only_online=?, next_run=? WHERE id=? AND server_id=?`,
			truncate(in.Name, 60), in.Cron, in.Action, truncate(in.Payload, 1000), in.Enabled, in.OnlyOnline, next, id, c.Server.ID)) == 0 {
			writeErr(w, r, ErrNotFound)
			return
		}
	} else {
		var count int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM schedules WHERE server_id=?`, c.Server.ID).Scan(&count)
		if count >= 25 {
			writeErr(w, r, errCode(409, "25 schedules maximum per server"))
			return
		}
		if _, err := a.db.Exec(`INSERT INTO schedules(server_id,name,cron,action,payload,enabled,only_online,next_run,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			c.Server.ID, truncate(in.Name, 60), in.Cron, in.Action, truncate(in.Payload, 1000), in.Enabled, in.OnlyOnline, next, now()); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	c.log(a, "server.schedule.save", in.Name+" ("+in.Cron+")")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) handleScheduleDelete(w http.ResponseWriter, r *http.Request, c *Ctx) {
	id, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	_, _ = a.db.Exec(`DELETE FROM schedules WHERE id=? AND server_id=?`, id, c.Server.ID)
	c.log(a, "server.schedule.delete", fmt.Sprintf("#%d", id))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) handleScheduleRun(w http.ResponseWriter, r *http.Request, c *Ctx) {
	id, _ := strconv.ParseInt(r.PathValue("sid"), 10, 64)
	rows, err := a.db.Query(`SELECT `+schedCols+` FROM schedules WHERE id=? AND server_id=?`, id, c.Server.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	list, _ := scanSchedules(rows)
	if len(list) == 0 {
		writeErr(w, r, ErrNotFound)
		return
	}
	res := a.runSchedule(list[0], true)
	c.log(a, "server.schedule.run", list[0].Name)
	writeJSON(w, 200, map[string]string{"result": res})
}

func (a *App) runSchedule(sc *Schedule, manual bool) string {
	s, err := a.db.Server(sc.ServerID)
	if err != nil {
		return "error: server not found"
	}
	result := "OK"
	st := a.kube.State(s.ID).Status
	switch {
	case s.Suspended:
		result = "skipped: suspended"
	case sc.OnlyOnline && st != "running" && !manual:
		result = "skipped: offline"
	default:
		var err error
		switch sc.Action {
		case "command":
			err = a.sendCommand(s, sc.Payload)
		case "backup":
			_, err = a.createBackup(s, sc.Payload, true)
		default:
			err = a.power(s, sc.Action)
		}
		if err != nil {
			result = "error: " + err.Error()
		}
	}
	switch {
	case result == "OK":
		a.hub(s.ID).daemon("Scheduled task “%s”: done", sc.Name)
	case strings.HasPrefix(result, "skipped"):
		a.hub(s.ID).daemon("Scheduled task “%s” skipped: the server is suspended or offline", sc.Name)
	default:
		a.hub(s.ID).daemonErr("Scheduled task “%s” failed: %s", sc.Name, strings.TrimPrefix(result, "error: "))
	}
	next, _ := a.nextRun(sc.Cron)
	_, _ = a.db.Exec(`UPDATE schedules SET last_run=?, next_run=?, last_result=? WHERE id=?`, now(), next, result, sc.ID)
	a.db.LogActivity(s.ID, nil, "", "server.schedule.execute", sc.Name+" → "+result)
	return result
}

func (a *App) scheduleLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := a.db.Query(`SELECT `+schedCols+` FROM schedules WHERE enabled=1 AND next_run>0 AND next_run<=?`, now())
		if err != nil {
			continue
		}
		due, err := scanSchedules(rows)
		if err != nil {
			continue
		}
		for _, sc := range due {
			a.runSchedule(sc, false)
		}
	}
}
