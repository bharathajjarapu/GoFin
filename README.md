# GoFin

GoFin is a small Go media server compatible with Jellyfin clients like Plezy.

Built for a simple home/LAN setup — Movies, TV shows and Music, direct playback, SQLite, TMDB metadata.

For build, local development, and production deployment with Podman or systemd, see [DEPLOY.md](DEPLOY.md).

## Features

- **Rich metadata** — posters, backdrops, cast (up to 15), crew, genres, studios, ratings, runtime, taglines, external links (TMDB/IMDb)
- **TV show support** — series, seasons, episodes with full metadata
- **Music support** — artists, albums and tracks read from embedded tags, with exact durations, cover art and lyrics parsed from container headers (no FFmpeg, no extra dependencies)
- **Playlists** — create, reorder and share playlists across the server
- **Suggestions** — "more like this" scored on shared genres, cast and crew, entirely from your own library
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
- **Jellyfin API compatibility** — endpoints for /Items, /Shows, /Artists, /Genres, /MusicGenres, /Studios, /Playlists, /Persons, /Search/Hints, /Sessions, /PlaybackInfo, streaming, images, and more
- **No transcoding** — direct-play only, no FFmpeg required

## Configuration

```json
{
  "libraries": [
    { "name": "Movies", "type": "movies", "path": "/data/movies" },
    { "name": "TV", "type": "tvshows", "path": "/data/tv" },
    { "name": "Music", "type": "music", "path": "/data/music" }
  ],
  "scan": {
    "on_start": false,
    "interval_minutes": 0
  }
}
```

Library `type` is `movies`, `tvshows` or `music`. Paths must be absolute and unique.

Set `TMDB_API_KEY` in the environment. Music needs no key — its metadata comes from the tags embedded in each file.

## Recommended naming

```text
Movies/Dune (2021).mkv
Movies/Dune Part Two (2024) [tmdbid-438631].mkv
TV/Example Show (2024)/Season 01/Example Show S01E01.mkv
Music/New Order/Power (1983)/01 Blue Monday.flac
```

## Music

Tracks are read straight from each file's embedded tags, so a well-tagged library needs no network calls and no API key.

- **Containers** — `flac`, `m4a`, `m4b`, `ogg`, `oga`, `opus`. All are direct-play friendly, which is the point: the server never transcodes. Audio in an MP4 container must be named `.m4a`, matching what Jellyfin expects; an `.mp4` in a music library is ignored.
- **Tags beat folders** — artist and album come from `ALBUMARTIST`/`ALBUM` (or their MP4 equivalents), so compilations and re-tagged files group correctly even when the directory names disagree. Folder names are only a fallback for untagged files.
- **Durations are exact**, parsed from the container header: FLAC `STREAMINFO`, Ogg granule positions, MP4 `mvhd`. No estimation, no probing.
- **Cover art** comes from `cover.jpg`, `folder.jpg` or `poster.jpg` in the album folder, and falls back to the art embedded in the tracks themselves — the same precedence Jellyfin applies. Embedded art is copied out once per album into a `covers/` folder beside the database, so serving it costs no parsing.
- **Lyrics** are read from `LYRICS`/`UNSYNCEDLYRICS` tags and MP4 `©lyr`. `[mm:ss.xx]` timestamps are understood, so synced lyrics scroll with playback; plain text is shown as a static sheet.
- **Multi-disc albums** use the `DISCNUMBER` tag. A track without one counts as disc 1, so a partly tagged album keeps a single running order.
- **Multiple artists** on one track come from repeated `ARTIST` tags or a semicolon-separated list. A slash is left alone, so AC/DC stays one band.

Clients stream from `/Audio/{id}/stream`, `/Audio/{id}/stream.{ext}` and `/Audio/{id}/universal`. All three return the original bytes with range support, so seeking works. Lyrics come from `/Audio/{id}/Lyrics`.

## User types

```sh
# Admin (full access)
printf '%s' 'choose-a-strong-password' | ./gofin user add --name admin --password-stdin --admin

# Child (content filtered by rating)
printf '%s' 'choose-a-strong-password' | ./gofin user add --name kid --password-stdin --child --max-rating 3
```

Rating levels: G=1, PG=2, PG-13/TV-14=3, R/TV-MA=4, NC-17=5.

## Performance

Same machine (4 cores), same library of 2,000 FLAC tracks, 500 movies and 200 episodes, and the same requests from 8 concurrent clients. Jellyfin 12.1.0 ran from its official image, with internet metadata and chapter/trickplay extraction turned off.

| | GoFin | Jellyfin |
|---|---|---|
| Memory, just started | 12 MB | 327 MB |
| Memory, idle after scan | 17 MB | 370 MB |
| Memory, peak under load | 56 MB | 422 MB |
| First scan | 12 s | 134 s |
| Rescan, nothing changed | 3 s | 5 s |
| Install size | 15 MB image | 1.7 GB image |
| Album tracks, median | 8 ms | 98 ms |
| Search, median | 7 ms | 103 ms |
| Latest movies, median | 9 ms | 61 ms |
| 100-movie grid, median | 29 ms | 78 ms |
| 100-album grid, median | 74 ms | 123 ms |

Jellyfin does more work for its numbers: it probes every file with FFmpeg and can transcode. GoFin reads only headers and tags, which is also why its scan is faster. Plex and Emby were not measured.

## Why not Jellyfin?

GoFin is not a full Jellyfin replacement. It intentionally omits:

- **Transcoding**
- **FFmpeg probing** — no codec or stream details, so clients show no resolution or audio-track badges and leave track selection to the player
- **Web interface** — use a client app such as Plezy
- **Plugin system**
- **Multiple metadata providers** (TMDB only)
- **Fuzzy metadata matching**
- **Live TV, DVR, SyncPlay and collections**
- **Live updates** — no websocket, so clients see new media on their next refresh
- **LAN discovery** — enter the server address by hand
- **Per-user client settings** — preferences a client tries to save on the server are not kept

For music specifically, it also omits:

- **Online music metadata** — no MusicBrainz or Last.fm, tags only
- **Instant Mix**

Playlists are shared across the server rather than owned per user, which suits a household and keeps the model small. Parental limits still apply to what a playlist contains, so a child account never sees restricted media inside a shared one.

Because nothing is transcoded, a client that cannot decode a container simply will not play it. FLAC and M4A play essentially everywhere; Opus depends on the client.

The goal is a small, fast, reliable direct-play server for movie, TV and music libraries.
