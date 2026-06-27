# GoFin

GoFin is a small Go media server that aims to be compatible with Jellyfin clients like Plezy.

It is built for a simple home/LAN setup:

- Movies and TV shows
- Direct playback only
- SQLite catalog
- TMDB metadata
- Jellyfin-style API responses
- No transcoding
- No FFmpeg requirement for now

## Status

GoFin is an early base build. It can scan media, store items in SQLite, authenticate users, and serve files directly to Jellyfin-compatible clients.

Current focus:

- rich movie metadata
- Plezy compatibility
- simple TV show browsing
- keeping the code small and easy to understand

## Requirements

- Go
- SQLite through `github.com/mattn/go-sqlite3`
- CGO enabled
- C compiler/build tools
- TMDB API key for metadata

On Linux/WSL, install build tools first:

```sh
sudo apt update
sudo apt install build-essential
```

## Configuration

Create a config file:

```sh
./gofin config init
```

Set your TMDB key in the environment instead of storing it in config:

```sh
export TMDB_API_KEY="your-tmdb-key"
```

Example library path:

```json
{
  "libraries": [
    {
      "name": "Movies",
      "type": "movies",
      "path": "/home/bharath/media/movies"
    }
  ]
}
```

## Build

```sh
CGO_ENABLED=1 go build -o gofin ./cmd/gofin
```

## Add a user

```sh
./gofin user add --config gofin.json --name bharath --password 5522
```

## Scan media

```sh
export TMDB_API_KEY="your-tmdb-key"
./gofin scan --config gofin.json
```

## Serve

```sh
./gofin serve --config gofin.json
```

Default address:

```text
http://127.0.0.1:8096
```

For LAN/Windows/WSL testing, use the configured public URL or your Windows LAN IP.

## Recommended naming

Use Jellyfin/Plex-style names for best metadata matching:

```text
Movies/Dune (2021).mkv
Movies/Dune Part Two (2024).mkv
TV Shows/Example Show (2024)/Season 01/Example Show S01E01.mkv
```

Provider IDs can be added later for exact matching:

```text
Dune (2021) [tmdbid-438631].mkv
```

## Test

```sh
CGO_ENABLED=1 go test ./...
```

## What GoFin does not do yet

- transcoding
- FFmpeg probing
- trickplay thumbnails
- fuzzy metadata matching
- music libraries
- full Jellyfin server replacement
- full Plex server API

The goal is to stay small, fast, and useful for direct-play movie and TV libraries.
