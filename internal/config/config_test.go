package config

import (
	"path/filepath"
	"testing"
)

func TestValidateLibraries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movies")
	tests := []struct {
		name string
		libs []Library
		want bool
	}{
		{name: "valid", libs: []Library{{Name: "Movies", Type: "movies", Path: path}}},
		{name: "relative path", libs: []Library{{Name: "Movies", Type: "movies", Path: "movies"}}, want: true},
		{name: "music", libs: []Library{{Name: "Music", Type: "music", Path: path}}},
		{name: "unknown type", libs: []Library{{Name: "Books", Type: "books", Path: path}}, want: true},
		{name: "duplicate path", libs: []Library{{Name: "One", Type: "movies", Path: path}, {Name: "Two", Type: "tvshows", Path: path}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateLibraries(test.libs); (err != nil) != test.want {
				t.Fatalf("validateLibraries() error = %v, want error %t", err, test.want)
			}
		})
	}
}
