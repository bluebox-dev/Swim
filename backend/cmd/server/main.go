// Command server is the SWIM control-plane backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"swim/backend/internal/api"
	"swim/backend/internal/builder"
	"swim/backend/internal/db"
	"swim/backend/internal/signing"
	"swim/backend/internal/vm"
	"swim/backend/internal/wol"
	"swim/backend/migrations"
	"swim/backend/plugin"
	"swim/pkg/wof"
)

var version = "0.1.0-dev"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return def
}

func main() {
	// `swim-server wof-check <ntfs-device> [--fix]`: find and decompress WOF (CompactOS)
	// files on an unmounted NTFS device — dry run by default. Capture does this itself.
	if len(os.Args) > 2 && os.Args[1] == "wof-check" {
		fix := len(os.Args) > 3 && os.Args[3] == "--fix"
		start := time.Now()
		logf := func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }
		st, err := wof.Uncompress(os.Args[2], env("SWIM_UNWOF", "/usr/local/bin/ntfs-unwof"), env("TMPDIR", "/tmp"), !fix, logf)
		fmt.Printf("wof files ok %d (%.2f GB → %.2f GB), PE checked %d, PE bad %d, left compressed %d (in System32/Boot: %d), %s, fix=%v err=%v\n",
			st.Files, float64(st.InBytes)/1e9, float64(st.OutBytes)/1e9, st.PEChecked, st.PEBad, st.Failed, st.SystemLeft(),
			time.Since(start).Round(time.Second), fix, err)
		if err != nil || st.SystemLeft() > 0 {
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	// `swim-server waker`: Wake-on-LAN sender on the host network (no DB), see internal/wol
	if len(os.Args) > 1 && os.Args[1] == "waker" {
		if err := wol.Serve(env("SWIM_WAKER_SOCK", "/run/swim/waker.sock"), log); err != nil {
			log.Error("waker", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	migrateDSN, appDSN := os.Getenv("MIGRATE_DSN"), os.Getenv("DATABASE_URL")
	if migrateDSN == "" || appDSN == "" {
		return errors.New("MIGRATE_DSN and DATABASE_URL are required")
	}
	if err := db.Migrate(ctx, migrateDSN, migrations.FS, log); err != nil {
		return err
	}
	// plugins that are linked in (plugins_gen.go): their own tables, after the core's
	for _, p := range plugin.All() {
		log.Info("plugin", "name", p.Name, "version", p.Version)
		if p.Migrations == nil {
			continue
		}
		names, _ := fs.Glob(p.Migrations, "*.sql")
		for _, n := range names {
			if !strings.HasPrefix(n, p.Name+"_") {
				return fmt.Errorf("plugin %s: migration %s must be named %s_*.sql", p.Name, n, p.Name)
			}
		}
		if err := db.Migrate(ctx, migrateDSN, p.Migrations, log); err != nil {
			return fmt.Errorf("plugin %s: %w", p.Name, err)
		}
	}
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	// `swim-server user add|reset-2fa <username>`: account bootstrap / break-glass over ssh
	if len(os.Args) > 1 && os.Args[1] == "user" {
		srv := api.New(pool, log, nil, api.Config{})
		link, err := srv.CLIUser(ctx, os.Args[2:])
		if err != nil {
			return err
		}
		base := env("SWIM_PUBLIC_URL", "https://<server address>")
		fmt.Printf("setup link (one-time, 24 h): %s%s\n", strings.TrimRight(base, "/"), link)
		return nil
	}
	lv := vm.Libvirt{URI: env("SWIM_LIBVIRT_URI", "qemu:///system"), SnapDir: env("SWIM_SNAP_DIR", "/srv/swim/vms/snapshots")}
	// `swim-server builder` runs the privileged task worker instead of the API.
	if len(os.Args) > 1 && os.Args[1] == "builder" {
		host, _ := os.Hostname()
		return (&builder.Worker{Pool: pool, Log: log, LV: lv, Storage: env("SWIM_STORAGE", "/storage"),
			ISODir: env("SWIM_ISO_DIR", "/srv/swim/vms/iso"), ID: "builder@" + host}).Run(ctx)
	}

	key, err := signing.LoadOrCreate(env("SWIM_MANIFEST_KEY", "/keys/manifest-ed25519.pem"))
	if err != nil {
		return err
	}
	srv := api.New(pool, log, key, api.Config{
		StorageDir: env("SWIM_STORAGE", "/storage"),
		Libvirt:    env("SWIM_LIBVIRT_URI", "qemu:///system"),
		Version:    version,
		GuacSecret: os.Getenv("GUAC_JSON_SECRET"),
		ISODir:     env("SWIM_ISO_DIR", "/srv/swim/vms/iso"),
		SnapDir:    lv.SnapDir,
		CertFile:   env("SWIM_CERT_FILE", "/certs/server.crt"),
		HeartbeatS: envInt("SWIM_HEARTBEAT_S", 60),
		TwoPerson:  os.Getenv("SWIM_TWO_PERSON") == "true",
		WakerSock:  env("SWIM_WAKER_SOCK", "/run/swim/waker.sock"),
	})
	if err := srv.SetAdmins(ctx, os.Getenv("SWIM_ADMIN_TOKENS")); err != nil {
		return err
	}
	srv.Run(ctx)

	hs := &http.Server{Addr: env("SWIM_LISTEN", ":8080"), Handler: srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("listening", "addr", hs.Addr, "version", version, "manifest_kid", key.Kid)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
