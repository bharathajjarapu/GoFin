package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
)

type Config struct {
	Server    Server    `json:"server"`
	Database  Database  `json:"database"`
	Libraries []Library `json:"libraries"`
	Scan      Scan      `json:"scan"`
	Metadata  Metadata  `json:"metadata"`
	Auth      Auth      `json:"auth"`
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
	OnStart bool `json:"on_start"`
	Workers int  `json:"workers"`
}

type Metadata struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider"`
	APIKeyEnv string `json:"api_key_env"`
	Language  string `json:"language"`
}

type Auth struct {
	AllowPublicUsers bool `json:"allow_public_users"`
}

func Default() Config {
	return Config{
		Server:    Server{Name: "GoFin", ID: randID(), Address: "0.0.0.0:8096"},
		Database:  Database{Path: "gofin.db"},
		Libraries: []Library{{Name: "Movies", Type: "movies", Path: "/media/Movies"}, {Name: "TV Shows", Type: "tvshows", Path: "/media/TV"}},
		Scan:      Scan{Workers: 2},
		Metadata:  Metadata{Enabled: true, Provider: "tmdb", APIKeyEnv: "TMDB_API_KEY", Language: "en-US"},
		Auth:      Auth{AllowPublicUsers: true},
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
	return c, nil
}

func SaveDefault(path string) error {
	c := Default()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
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
	if c.Scan.Workers <= 0 {
		c.Scan.Workers = 2
	}
	if c.Metadata.Provider == "" {
		c.Metadata.Provider = "tmdb"
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
