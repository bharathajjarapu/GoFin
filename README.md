# GoFin

GoFin is a small Go media server compatible with Jellyfin clients like Plezy.

Built for a simple home/LAN setup — Movies and TV shows, direct playback, SQLite, TMDB metadata.

## Quick start

```sh
# Build
go build -o gofin ./cmd/gofin

# Init config and set your TMDB key
./gofin config init
export TMDB_API_KEY="your-tmdb-key"

# Add a user (admin by default, or use --child for restricted access)
./gofin user add --name bharath --password 5522

# Scan libraries
./gofin scan --config gofin.json

# Serve
./gofin serve --config gofin.json
# → http://127.0.0.1:8096
```

## Features

- **Rich metadata** — posters, backdrops, cast (up to 15), crew, genres, studios, ratings, runtime, taglines, external links (TMDB/IMDb)
- **TV show support** — series, seasons, episodes with full metadata
- **Smart scanning** — incremental rescans (skips unchanged files by size+mtime), auto-removes deleted files, background scan loop
- **Exact provider IDs** — `[tmdbid-438631]`, `[imdbid-tt1160419]`, `[tvdbid-12345]` in filenames for zero-guess matching
- **People database** — cast/crew stored in SQLite with TMDB profile images, lazy-loaded biographies
- **Search** — ranked `/Search/Hints` across items and people with prefix-priority matching
- **User system** — admin accounts, child accounts with parental rating controls (G→NC-17), per-user favorites and playback state
- **Sessions** — device-aware auth tokens, session listing, logout
- **Playback progress** — resume, played/unplayed tracking, play count
- **Favorites** — add/remove favorites, filter by favorites
- **Next Up** — unwatched episode queue for TV shows
- **Filters** — by genre, rating, year, person, name prefix, favorites, played state
- **Jellyfin API compatibility** — endpoints for /Items, /Shows, /Persons, /Search/Hints, /Sessions, /PlaybackInfo, streaming, images, and more
- **No transcoding** — direct-play only, no FFmpeg required

## Configuration

```json
{
  "libraries": [
    { "name": "Movies", "type": "movies", "path": "/data/movies" },
    { "name": "TV", "type": "tvshows", "path": "/data/tv" }
  ],
  "scan": {
    "on_start": false,
    "interval_minutes": 0,
    "workers": 4
  }
}
```

Set `TMDB_API_KEY` in the environment.

## Recommended naming

```text
Movies/Dune (2021).mkv
Movies/Dune Part Two (2024) [tmdbid-438631].mkv
TV/Example Show (2024)/Season 01/Example Show S01E01.mkv
```

## User types

```sh
# Admin (full access)
./gofin user add --name admin --password pass --admin

# Child (content filtered by rating)
./gofin user add --name kid --password pass --child --max-rating 3
```

Rating levels: G=1, PG=2, PG-13/TV-14=3, R/TV-MA=4, NC-17=5.

## Why not Jellyfin?

GoFin is not a full Jellyfin replacement. It intentionally omits:

- **Transcoding**
- **FFmpeg probing** (no codec/stream analysis yet)
- **Music libraries**
- **Plugin system**
- **Multiple metadata providers** (TMDB only)
- **Fuzzy metadata matching**

The goal is a small, fast, reliable direct-play server for movie and TV libraries.
