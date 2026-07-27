package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"gofin/internal/config"
	"gofin/internal/httpapi"
	"gofin/internal/library"
	"gofin/internal/metadata"
	"gofin/internal/store"
)

var errUsage = errors.New("usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	if err := run(ctx, os.Args[1], os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	if errors.Is(err, errUsage) {
		return 2
	}
	return 1
}

func run(ctx context.Context, cmd string, args []string, in io.Reader, out, errOut io.Writer) error {
	switch cmd {
	case "config":
		if len(args) == 0 || args[0] != "init" {
			usage(errOut)
			return errUsage
		}
		return initConfig(args[1:], errOut)
	case "version":
		if len(args) != 0 {
			return errUsage
		}
		fmt.Fprintln(out, "GoFin 0.1.0")
		return nil
	case "user":
		if len(args) > 0 && args[0] == "add" {
			return addUser(args[1:], in, errOut)
		}
	case "scan":
		return scan(ctx, args, errOut)
	case "serve":
		return serve(ctx, args, errOut)
	}
	usage(errOut)
	return errUsage
}

func initConfig(args []string, errOut io.Writer) error {
	fs := flag.NewFlagSet("config init", flag.ContinueOnError)
	fs.SetOutput(errOut)
	path := fs.String("config", "gofin.json", "config path")
	force := fs.Bool("force", false, "overwrite an existing config")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errUsage
	}
	return config.SaveDefault(*path, *force)
}

func open(path, dbPath string) (config.Config, *store.Store, error) {
	c, err := config.Load(path)
	if err != nil {
		return c, nil, err
	}
	if dbPath != "" {
		c.Database.Path = dbPath
	}
	s, err := store.Open(c.Database.Path)
	if err != nil {
		return c, nil, err
	}
	return c, s, nil
}

func addUser(args []string, in io.Reader, errOut io.Writer) error {
	fs := flag.NewFlagSet("user add", flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfg := fs.String("config", "gofin.json", "config path")
	name := fs.String("name", "", "user name")
	pass := fs.String("password", "", "password")
	passwordStdin := fs.Bool("password-stdin", false, "read password from standard input")
	admin := fs.Bool("admin", true, "administrator user")
	child := fs.Bool("child", false, "hide media above --max-rating")
	maxRating := fs.Int("max-rating", 0, "max parental rating score: G=1 PG=2 PG-13/TV-14=3 R/TV-MA=4 NC-17=5")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errUsage
	}
	if *passwordStdin && *pass != "" {
		return fmt.Errorf("--password and --password-stdin cannot be used together")
	}
	if *passwordStdin {
		b, err := io.ReadAll(io.LimitReader(in, 4097))
		if err != nil {
			return err
		}
		if len(b) > 4096 {
			return fmt.Errorf("password is too long")
		}
		*pass = strings.TrimSpace(string(b))
	}
	if *name == "" || *pass == "" {
		return fmt.Errorf("--name and --password are required")
	}
	_, s, err := open(*cfg, "")
	if err != nil {
		return err
	}
	defer s.Close()
	return s.AddUserPolicy(*name, *pass, *admin, *child, *maxRating)
}

func scan(ctx context.Context, args []string, errOut io.Writer) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfg := fs.String("config", "gofin.json", "config path")
	only := fs.String("library", "", "library name or id")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errUsage
	}
	c, s, err := open(*cfg, "")
	if err != nil {
		return err
	}
	defer s.Close()
	return library.Scanner{Store: s, Meta: metadata.New(c.Metadata)}.ScanContext(ctx, c.Libraries, *only)
}

func serve(ctx context.Context, args []string, errOut io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfg := fs.String("config", "gofin.json", "config path")
	addr := fs.String("addr", "", "listen address")
	db := fs.String("db", "", "database path")
	scanStart := fs.Bool("scan-on-start", false, "scan before serving")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errUsage
	}
	c, s, err := open(*cfg, *db)
	if err != nil {
		return err
	}
	defer s.Close()
	if *addr != "" {
		c.Server.Address = *addr
	}
	meta := metadata.New(c.Metadata)
	if *scanStart || c.Scan.OnStart {
		if err := runScan(ctx, s, c, meta); err != nil {
			return err
		}
	}
	if c.Scan.IntervalMinutes > 0 {
		go scanLoop(ctx, s, c, meta)
	}
	log.Println("listening on", c.Server.Address)
	server := &http.Server{
		Addr:              c.Server.Address,
		Handler:           httpapi.API{C: c, S: s, Meta: meta}.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

var scanMu sync.Mutex

func runScan(ctx context.Context, s *store.Store, c config.Config, meta metadata.Client) error {
	scanMu.Lock()
	defer scanMu.Unlock()
	return (library.Scanner{Store: s, Meta: meta}).ScanContext(ctx, c.Libraries, "")
}

func scanLoop(ctx context.Context, s *store.Store, c config.Config, meta metadata.Client) {
	t := time.NewTicker(time.Duration(c.Scan.IntervalMinutes) * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !scanMu.TryLock() {
			continue
		}
		log.Println("background scan started")
		if err := (library.Scanner{Store: s, Meta: meta}).ScanContext(ctx, c.Libraries, ""); err != nil {
			log.Println("background scan:", err)
		}
		log.Println("background scan finished")
		scanMu.Unlock()
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: gofin config init | user add | scan | serve | version")
}
