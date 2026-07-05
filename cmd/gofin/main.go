package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"gofin/internal/config"
	"gofin/internal/httpapi"
	"gofin/internal/library"
	"gofin/internal/metadata"
	"gofin/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		log.Fatal(err)
	}
}

func run(cmd string, args []string) error {
	switch cmd {
	case "config":
		if len(args) > 0 && args[0] == "init" {
			return config.SaveDefault("gofin.json")
		}
	case "version":
		fmt.Println("GoFin 0.1.0")
		return nil
	case "user":
		if len(args) > 0 && args[0] == "add" {
			return addUser(args[1:])
		}
	case "scan":
		return scan(args)
	case "serve":
		return serve(args)
	}
	usage()
	return nil
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

func addUser(args []string) error {
	fs := flag.NewFlagSet("user add", flag.ExitOnError)
	cfg := fs.String("config", "gofin.json", "config path")
	name := fs.String("name", "", "user name")
	pass := fs.String("password", "", "password")
	admin := fs.Bool("admin", true, "administrator user")
	child := fs.Bool("child", false, "hide media above --max-rating")
	maxRating := fs.Int("max-rating", 0, "max parental rating score: G=1 PG=2 PG-13/TV-14=3 R/TV-MA=4 NC-17=5")
	_ = fs.Parse(args)
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

func scan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	cfg := fs.String("config", "gofin.json", "config path")
	only := fs.String("library", "", "library name or id")
	_ = fs.Parse(args)
	c, s, err := open(*cfg, "")
	if err != nil {
		return err
	}
	defer s.Close()
	return library.Scanner{Store: s, Meta: metadata.New(c.Metadata)}.Scan(c.Libraries, *only)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfg := fs.String("config", "gofin.json", "config path")
	addr := fs.String("addr", "", "listen address")
	db := fs.String("db", "", "database path")
	scanStart := fs.Bool("scan-on-start", false, "scan before serving")
	_ = fs.Parse(args)
	c, s, err := open(*cfg, *db)
	if err != nil {
		return err
	}
	defer s.Close()
	if *addr != "" {
		c.Server.Address = *addr
	}
	if *scanStart || c.Scan.OnStart {
		if err := runScan(s, c); err != nil {
			return err
		}
	}
	if c.Scan.IntervalMinutes > 0 {
		go scanLoop(s, c)
	}
	log.Println("listening on", c.Server.Address)
	return http.ListenAndServe(c.Server.Address, httpapi.API{C: c, S: s}.Handler())
}

var scanMu sync.Mutex

func runScan(s *store.Store, c config.Config) error {
	scanMu.Lock()
	defer scanMu.Unlock()
	return (library.Scanner{Store: s, Meta: metadata.New(c.Metadata)}).Scan(c.Libraries, "")
}

func scanLoop(s *store.Store, c config.Config) {
	t := time.NewTicker(time.Duration(c.Scan.IntervalMinutes) * time.Minute)
	defer t.Stop()
	for range t.C {
		if !scanMu.TryLock() {
			continue
		}
		log.Println("background scan started")
		if err := (library.Scanner{Store: s, Meta: metadata.New(c.Metadata)}).Scan(c.Libraries, ""); err != nil {
			log.Println("background scan:", err)
		}
		log.Println("background scan finished")
		scanMu.Unlock()
	}
}

func usage() { fmt.Println("usage: gofin config init | user add | scan | serve | version") }
