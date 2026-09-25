# GoFin

GoFin is a small Go media server for Jellyfin clients such as Plezy. It serves movies, TV and music from a home network, plays files directly without transcoding, stores everything in SQLite and fetches movie and TV metadata from TMDB.

To build and deploy it, see [DEPLOY.md](DEPLOY.md). To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Features

- Movie and TV metadata from TMDB, including posters, cast, crew, genres, ratings and links. A `[tmdbid-438631]`, `[imdbid-tt1160419]` or `[tvdbid-12345]` tag in a filename skips the name search.
- Music read from the tags inside each file, with exact durations, cover art and synced lyrics. It needs no FFmpeg and no API key.
- Series, seasons, episodes and a Next Up queue.
- Resume points, played state, play counts and favorites for each user.
- Shared playlists you can reorder.
- Suggestions scored on shared genres, cast and crew in your own library.
- Search across media and people.
- Filters by genre, rating, year, person, name, favorites and played state.
- Child accounts limited by parental rating.
- Rescans that skip unchanged files and drop deleted ones, on a schedule if you want.

## Configuration

```json
{
  "libraries": [
    { "name": "Movies", "type": "movies", "path": "/data/movies" },
    { "name": "TV", "type": "tvshows", "path": "/data/tv" },
    { "name": "Music", "type": "music", "path": "/data/music" }
  ],
  "scan": { "on_start": false, "interval_minutes": 0 }
}
```

A library `type` is `movies`, `tvshows` or `music`. Paths must be absolute and unique. Set `TMDB_API_KEY` in the environment for movies and TV.

## Naming

```text
Movies/Dune (2021).mkv
Movies/Dune Part Two (2024) [tmdbid-438631].mkv
TV/Example Show (2024)/Season 01/Example Show S01E01.mkv
Music/New Order/Power (1983)/01 Blue Monday.flac
```

## Music

GoFin reads `flac`, `m4a`, `m4b`, `ogg`, `oga` and `opus` files. It ignores `.mp4` in a music library, so name MP4 audio `.m4a`.

- Artist and album come from the `ALBUMARTIST` and `ALBUM` tags. Folder names count only when a file has no tags.
- Durations come from the file header, so they are exact.
- Cover art comes from `cover.jpg`, `folder.jpg` or `poster.jpg` in the album folder. Without one, GoFin copies the art embedded in a track into a `covers/` folder beside the database.
- Lyrics come from the `LYRICS` and `UNSYNCEDLYRICS` tags and MP4 `©lyr`. Lines with `[mm:ss.xx]` timestamps scroll with playback.
- `DISCNUMBER` orders multi-disc albums. A track without it counts as disc 1.
- Repeated `ARTIST` tags or a semicolon-separated list give a track several artists. A slash does not split, so AC/DC stays one band.

## Users

```sh
# Administrator
printf '%s' 'choose-a-strong-password' | ./gofin user add --name admin --password-stdin

# Child account that sees PG-13 and below
printf '%s' 'choose-a-strong-password' | ./gofin user add --name kid --password-stdin --child --max-rating 3
```

Rating levels are G=1, PG=2, PG-13 and TV-14=3, R and TV-MA=4, NC-17=5.

## Performance

Both servers ran on the same 4-core machine with the same library of 2,000 FLAC tracks, 500 movies and 200 episodes, and served the same requests from 8 concurrent clients. Jellyfin 12.1.0 ran from its official image with internet metadata and chapter and trickplay extraction turned off.

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

Jellyfin probes every file with FFmpeg and can transcode, and GoFin does neither. That accounts for part of the gap. This comparison does not include Plex or Emby.

## Limitations

GoFin does not have:

- Transcoding. A client that cannot decode a file will not play it. FLAC and M4A play almost everywhere, and Opus depends on the client.
- Codec details. Clients show no resolution or audio track badges and leave track selection to the player.
- A web interface. Use a client app such as Plezy.
- Live TV, DVR, SyncPlay, collections or plugins.
- Live updates. Clients see new media on their next refresh.
- LAN discovery. Enter the server address by hand.
- Per-user client settings. GoFin does not keep preferences a client saves to the server.
- Metadata sources besides TMDB, fuzzy matching, MusicBrainz, Last.fm or Instant Mix.

Playlists belong to the whole server, not to one user. Child accounts still see only the allowed items inside them.
