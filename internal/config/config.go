package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	Server    Server    `json:"server"`
	Database  Database  `json:"database"`
	Libraries []Library `json:"libraries"`
	Scan      Scan      `json:"scan"`
	Metadata  Metadata  `json:"metadata"`
}

type Server struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Address   string `json:"address"`
	PublicURL string `json:"public_url"`
}

type Database struct {
	Path string `json:"path"`
}

type Library struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path"`
}

type Scan struct {
	OnStart         bool `json:"on_start"`
	IntervalMinutes int  `json:"interval_minutes"`
}

type Metadata struct {
	Enabled   bool   `json:"enabled"`
	APIKeyEnv string `json:"api_key_env"`
	Language  string `json:"language"`
}

func Default() Config {
	return Config{
		Server:    Server{Name: "GoFin", ID: randID(), Address: "0.0.0.0:8096"},
		Database:  Database{Path: "gofin.db"},
		Libraries: []Library{{Name: "Movies", Type: "movies", Path: "/media/Movies"}, {Name: "TV Shows", Type: "tvshows", Path: "/media/TV"}, {Name: "Music", Type: "music", Path: "/media/Music"}},
		Metadata:  Metadata{Enabled: true, APIKeyEnv: "TMDB_API_KEY", Language: "en-US"},
	}
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	Fill(&c)
	if c.Database.Path == "" {
		return c, errors.New("database.path is required")
	}
	if err := validateLibraries(c.Libraries); err != nil {
		return c, err
	}
	return c, nil
}

// libraryTypes are the collection types a library may declare. They match the
// Jellyfin collection names clients expect back from /UserViews.
var libraryTypes = map[string]bool{"movies": true, "tvshows": true, "music": true}

func validateLibraries(libraries []Library) error {
	paths := map[string]bool{}
	for _, library := range libraries {
		if library.Name == "" || library.Path == "" {
			return errors.New("library name and path are required")
		}
		if !filepath.IsAbs(library.Path) {
			return errors.New("library paths must be absolute")
		}
		if !libraryTypes[library.Type] {
			return errors.New("library type must be movies, tvshows or music")
		}
		path := filepath.Clean(library.Path)
		if paths[path] {
			return errors.New("library paths must be unique")
		}
		paths[path] = true
	}
	return nil
}

func SaveDefault(path string, force bool) error {
	c := Default()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func Fill(c *Config) {
	if c.Server.Name == "" {
		c.Server.Name = "GoFin"
	}
	if c.Server.ID == "" {
		c.Server.ID = randID()
	}
	if c.Server.Address == "" {
		c.Server.Address = "0.0.0.0:8096"
	}
	if c.Database.Path == "" {
		c.Database.Path = "gofin.db"
	}
	if c.Metadata.APIKeyEnv == "" {
		c.Metadata.APIKeyEnv = "TMDB_API_KEY"
	}
	if c.Metadata.Language == "" {
		c.Metadata.Language = "en-US"
	}
}

func randID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
