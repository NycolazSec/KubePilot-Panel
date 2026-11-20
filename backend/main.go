package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata"
)

//go:embed all:web
var webFS embed.FS

var version = "dev" // set at build time: -ldflags "-X main.version=…"

type Config struct {
	Listen        string
	DataDir       string
	Kubeconfig    string
	Namespace     string
	StorageRoot   string
	StorageClass  string
	SecureCookies bool
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.Listen, "listen", env("KUBEPILOT_LISTEN", ":8080"), "HTTP listen address")
	flag.StringVar(&cfg.DataDir, "data", env("KUBEPILOT_DATA", "/data"), "data directory (database, backups)")
	flag.StringVar(&cfg.Kubeconfig, "kubeconfig", env("KUBECONFIG", ""), "kubeconfig path (empty = in-cluster)")
	flag.StringVar(&cfg.Namespace, "namespace", env("KUBEPILOT_NAMESPACE", "kubepilot-servers"), "namespace for game servers")
	flag.StringVar(&cfg.StorageRoot, "storage-root", env("KUBEPILOT_STORAGE_ROOT", "/var/lib/rancher/k3s/storage"), "host path of local-path volumes")
	flag.StringVar(&cfg.StorageClass, "storage-class", env("KUBEPILOT_STORAGE_CLASS", "local-path"), "StorageClass of game server volumes (hostPath-based provisioner)")
	secure := flag.Bool("secure-cookies", env("KUBEPILOT_SECURE_COOKIES", "true") == "true", "set Secure flag on cookies")
	flag.Parse()
	cfg.SecureCookies = *secure

	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "backups"), 0o700); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	db, err := openDB(filepath.Join(cfg.DataDir, "kubepilot.db"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	if args := flag.Args(); len(args) > 0 {
		if err := runCLI(db, args); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	kube, err := newKube(cfg)
	if err != nil {
		log.Fatalf("kubernetes: %v", err)
	}
	app := newApp(cfg, db, kube)
	if err := app.seedTemplates(); err != nil {
		log.Fatalf("seed templates: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := kube.start(ctx); err != nil {
		log.Fatalf("kubernetes start: %v", err)
	}
	app.startBackground(ctx)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.routes(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()
	log.Printf("KubePilot %s listening on %s (namespace %s)", version, cfg.Listen, cfg.Namespace)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func runCLI(db *DB, args []string) error {
	switch args[0] {
	case "user:create":
		fs := flag.NewFlagSet("user:create", flag.ExitOnError)
		username := fs.String("username", "", "")
		email := fs.String("email", "", "")
		password := fs.String("password", "", "")
		admin := fs.Bool("admin", false, "")
		_ = fs.Parse(args[1:])
		if *username == "" || *email == "" || *password == "" {
			return errors.New("usage: user:create --username U --email E --password P [--admin]")
		}
		u, err := db.CreateUser(*username, *email, *password, *admin)
		if err != nil {
			return err
		}
		fmt.Printf("User created: %s (id %d, admin=%v)\n", u.Username, u.ID, u.IsAdmin)
	case "user:password":
		fs := flag.NewFlagSet("user:password", flag.ExitOnError)
		username := fs.String("username", "", "")
		password := fs.String("password", "", "")
		_ = fs.Parse(args[1:])
		u, err := db.UserByLogin(*username)
		if err != nil {
			return err
		}
		if err := db.SetPassword(u.ID, *password); err != nil {
			return err
		}
		fmt.Println("Password updated for", u.Username)
	case "user:2fa-reset":
		fs := flag.NewFlagSet("user:2fa-reset", flag.ExitOnError)
		username := fs.String("username", "", "")
		_ = fs.Parse(args[1:])
		u, err := db.UserByLogin(*username)
		if err != nil {
			return err
		}
		if err := db.DisableTOTP(u.ID); err != nil {
			return err
		}
		db.LogActivity("", nil, "", "admin.user.2fa.reset", u.Username)
		fmt.Println("Two-factor authentication disabled for", u.Username)
	default:
		return fmt.Errorf("unknown command %q (user:create, user:password, user:2fa-reset)", args[0])
	}
	return nil
}
