package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

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
	_ = fs.Parse(args)
	if *name == "" || *pass == "" {
		return fmt.Errorf("--name and --password are required")
	}
	_, s, err := open(*cfg, "")
	if err != nil {
		return err
	}
	defer s.Close()
	return s.AddUser(*name, *pass)
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
		if err := (library.Scanner{Store: s, Meta: metadata.New(c.Metadata)}).Scan(c.Libraries, ""); err != nil {
			return err
		}
	}
	log.Println("listening on", c.Server.Address)
	return http.ListenAndServe(c.Server.Address, httpapi.API{C: c, S: s}.Handler())
}

func usage() { fmt.Println("usage: gofin config init | user add | scan | serve | version") }
