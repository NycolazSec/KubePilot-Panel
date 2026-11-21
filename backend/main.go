package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"
)

//go:embed all:web
var webFS embed.FS

var version = "dev" // set at build time: -ldflags "-X main.version=…"

type Config struct {
	Listen       string
	DataDir      string
	Kubeconfig   string
	Namespace    string
	StorageRoot  string
	StorageClass string
	// MinecraftRouter: the chart deployed mc-router, which routes players by hostname on port 25565
	MinecraftRouter bool
	// AutoSleep: mc-router may start sleeping servers (the chart gave it the right to scale them)
	AutoSleep bool
	// RootInstall: the game namespace allows root install containers (Pod Security "baseline"), for eggs
	RootInstall bool
	// Ingress: the ingress controller's pods, that game servers may reach on its web ports (the cluster's own websites)
	Ingress IngressPeer
	// SFTPListen: address of the built-in SFTP server ("" = off); SFTPPort: the port users connect to
	SFTPListen string
	SFTPPort   int
	// DatabaseAddr / DatabasePassword: the MariaDB for the servers' databases (root), "" = off
	DatabaseAddr     string
	DatabasePassword string
	S3               S3Config   // backups on S3 when Endpoint is set
	Mail             MailConfig // emails (password reset, notifications) when Host is set
	PublicURL        string     // https://panel.example.com: the panel's own address, for links in emails
	SecureCookies    bool
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
	flag.BoolVar(&cfg.RootInstall, "root-install", env("KUBEPILOT_ROOT_INSTALL", "false") == "true", "install scripts of egg templates may run as root (namespace Pod Security baseline)")
	flag.BoolVar(&cfg.MinecraftRouter, "minecraft-router", env("KUBEPILOT_MINECRAFT_ROUTER", "false") == "true", "mc-router is deployed: give Minecraft servers hostnames on port 25565")
	flag.BoolVar(&cfg.AutoSleep, "auto-sleep", env("KUBEPILOT_AUTO_SLEEP", "false") == "true", "mc-router can wake servers up: empty Minecraft servers may go to sleep")
	flag.StringVar(&cfg.SFTPListen, "sftp-listen", env("KUBEPILOT_SFTP_LISTEN", ""), "SFTP listen address, e.g. :2022 (empty = SFTP off)")
	flag.IntVar(&cfg.SFTPPort, "sftp-port", atoiOr(env("KUBEPILOT_SFTP_PORT", ""), 0), "SFTP port shown to users (default: the listen port)")
	flag.StringVar(&cfg.DatabaseAddr, "database-addr", env("KUBEPILOT_DATABASE_ADDR", ""), "MariaDB host:port for the servers' databases (empty = off)")
	cfg.DatabasePassword = os.Getenv("KUBEPILOT_DATABASE_PASSWORD") // never a flag: it would show in the process list
	cfg.S3 = S3Config{Endpoint: os.Getenv("KUBEPILOT_S3_ENDPOINT"), Region: os.Getenv("KUBEPILOT_S3_REGION"), Bucket: os.Getenv("KUBEPILOT_S3_BUCKET"),
		Prefix: env("KUBEPILOT_S3_PREFIX", "kubepilot"), Insecure: os.Getenv("KUBEPILOT_S3_INSECURE") == "true",
		AccessKey: os.Getenv("KUBEPILOT_S3_ACCESS_KEY"), SecretKey: os.Getenv("KUBEPILOT_S3_SECRET_KEY"), EncryptionKey: os.Getenv("KUBEPILOT_S3_ENCRYPTION_KEY")}
	cfg.Mail = MailConfig{Host: os.Getenv("KUBEPILOT_SMTP_HOST"), Port: atoiOr(os.Getenv("KUBEPILOT_SMTP_PORT"), 0), TLS: os.Getenv("KUBEPILOT_SMTP_TLS"),
		Username: os.Getenv("KUBEPILOT_SMTP_USERNAME"), Password: os.Getenv("KUBEPILOT_SMTP_PASSWORD"), From: os.Getenv("KUBEPILOT_MAIL_FROM")}
	flag.StringVar(&cfg.PublicURL, "public-url", env("KUBEPILOT_PUBLIC_URL", ""), "the panel's public address (https://…), for links in emails")
	ingressNS := flag.String("ingress-namespace", env("KUBEPILOT_INGRESS_NAMESPACE", ""), "namespace of the ingress controller game servers may reach (empty = none)")
	ingressLabels := flag.String("ingress-pod-labels", env("KUBEPILOT_INGRESS_POD_LABELS", ""), "labels of the ingress controller's pods, e.g. app.kubernetes.io/name=traefik")
	ingressPorts := flag.String("ingress-ports", env("KUBEPILOT_INGRESS_PORTS", ""), "its ports game servers may reach (names or numbers), e.g. web,websecure")
	secure := flag.Bool("secure-cookies", env("KUBEPILOT_SECURE_COOKIES", "true") == "true", "set Secure flag on cookies")
	flag.Parse()
	cfg.SecureCookies = *secure
	var err error
	if cfg.Ingress, err = parseIngressPeer(*ingressNS, *ingressLabels, *ingressPorts); err != nil {
		log.Fatalf("ingress controller: %v", err)
	}
	if cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/"); cfg.PublicURL != "" {
		if u, err := url.Parse(cfg.PublicURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" {
			log.Fatalf("public URL %q: expected https://panel.example.com", cfg.PublicURL)
		}
	}

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
	if cfg.S3.Endpoint != "" {
		s, err := newS3Store(cfg.S3)
		if err != nil {
			log.Fatal(err)
		}
		app.s3 = s
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := s.check(cctx); err != nil {
			log.Printf("WARNING: S3 bucket %q on %s not reachable yet: %v", s.bucket, s.endpoint, err)
		} else {
			log.Printf("Backups go to S3: %s/%s (encrypted: %v)", s.endpoint, s.bucket, s.identity != nil)
		}
		cancel()
	}
	if m, err := newMailer(cfg.Mail); err != nil {
		log.Printf("WARNING: emails are off: %v", err)
	} else if m != nil {
		app.mail = m
		log.Printf("Emails: %s:%d (%s) as %s", m.cfg.Host, m.cfg.Port, m.cfg.TLS, m.from.Address)
	}
	app.startBackground(ctx)

	if cfg.SFTPListen != "" {
		s, err := newSFTPServer(app, filepath.Join(cfg.DataDir, "sftp_host_ed25519_key"))
		if err != nil {
			log.Fatal(err)
		}
		ln, err := net.Listen("tcp", cfg.SFTPListen)
		if err != nil {
			log.Fatalf("sftp: %v", err)
		}
		if app.cfg.SFTPPort == 0 {
			app.cfg.SFTPPort = ln.Addr().(*net.TCPAddr).Port
		}
		app.sftp = s
		go s.serve(ctx, ln)
		log.Printf("SFTP listening on %s (host key %s)", ln.Addr(), s.fingerprint)
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.routes(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
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

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
