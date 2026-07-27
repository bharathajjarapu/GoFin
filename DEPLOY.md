# Deploy GoFin

Use either deployment path below. The same `Dockerfile` works with Podman: a Dockerfile is the image recipe, and Podman can build it without running the Docker daemon.

For a small home server, direct systemd is the fewest moving parts. Podman is cleaner if you want container isolation and easier migration to another machine. Install Go first using the build instructions below only when building the direct systemd binary or a local container image. Release images already contain the server binary.

## Build and run

This project requires Go 1.26 or newer. On Debian/Ubuntu, install the distribution package and confirm that it meets that requirement:

```sh
sudo apt update
sudo apt install -y golang-go
go version
```

If `go version` is older than 1.26, install the official Go 1.26 archive instead (use the matching archive for your CPU architecture):

```sh
GO_ARCHIVE=go1.26.5.linux-amd64.tar.gz
wget "https://go.dev/dl/$GO_ARCHIVE"
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "$GO_ARCHIVE"
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.profile
. ~/.profile
go version
```

Build the development binary from the repository root:

```sh
go build -o gofin ./cmd/gofin
```

Create folders for your media and generate the initial configuration. `config init` creates a unique, persistent server ID; keep that ID when editing the file.

```sh
mkdir -p media/movies media/tv
./gofin config init
```

Edit `gofin.json` with the library paths from the [configuration reference](README.md#configuration). Use absolute paths and place movie files in `media/movies` and TV episodes in `media/tv`; see [Recommended naming](README.md#recommended-naming).

Start a local server after setting your TMDB key and creating the first administrator:

```sh
export TMDB_API_KEY="your-tmdb-key"
./gofin user add --config gofin.json --name admin --password 'choose-a-strong-password'
./gofin scan --config gofin.json
./gofin serve --config gofin.json
```

Open `http://127.0.0.1:8096` locally, or `http://SERVER-IP:8096` from another device on your LAN.

## Shared layout

Examples below use:

```text
/srv/gofin/gofin.json  # config
/srv/gofin/gofin.db    # SQLite database
/srv/gofin/gofin.env   # secrets
/srv/media/movies              # movies
/srv/media/tv                  # TV shows
```

If your hard disk is mounted somewhere else, use that path instead. For example, if `/dev/sdb1` is mounted at `/mnt/media`, set the media paths to `/mnt/media/movies` and `/mnt/media/tv` for direct systemd, or mount `/mnt/media:/media:ro` for Podman.

Create the app directory and secret file:

```sh
sudo mkdir -p /srv/gofin /srv/media/movies /srv/media/tv
sudo tee /srv/gofin/gofin.env >/dev/null <<'EOF'
TMDB_API_KEY=your-tmdb-key
EOF
sudo chmod 600 /srv/gofin/gofin.env
```

Example config for Podman:

```json
{
  "server": { "name": "GoFin", "address": "0.0.0.0:8096" },
  "database": { "path": "/config/gofin.db" },
  "libraries": [
    { "name": "Movies", "type": "movies", "path": "/media/movies" },
    { "name": "TV Shows", "type": "tvshows", "path": "/media/tv" }
  ],
  "scan": { "on_start": true, "interval_minutes": 60 },
  "metadata": { "enabled": true, "api_key_env": "TMDB_API_KEY", "language": "en-US" }
}
```

For direct systemd, use the same config but set:

```json
"database": { "path": "/srv/gofin/gofin.db" },
"libraries": [
  { "name": "Movies", "type": "movies", "path": "/srv/media/movies" },
  { "name": "TV Shows", "type": "tvshows", "path": "/srv/media/tv" }
]
```

Save the final config at `/srv/gofin/gofin.json`.

## Podman

The Dockerfile is runtime-only: it copies a static binary from `dist/gofin_linux_<architecture>` into a small distroless image. Build that binary first when testing an image locally:

```sh
arch=$(go env GOARCH)
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w" \
  -o "dist/gofin_linux_$arch" ./cmd/gofin
sudo podman build --build-arg TARGETARCH="$arch" -t gofin:local .
```

For a published release image, replace `gofin:local` below with `ghcr.io/OWNER/gofin:VERSION`. Release images support `linux/amd64` and `linux/arm64`; Podman selects the matching image automatically.

Run once:

```sh
sudo chown -R 65532:65532 /srv/gofin
sudo podman run --rm \
  --name gofin \
  -p 8096:8096 \
  --env-file /srv/gofin/gofin.env \
  -v /srv/gofin:/config \
  -v /srv/media:/media:ro \
  gofin:local
```

Install as a system service with Quadlet:

```sh
sudo chown -R 65532:65532 /srv/gofin
sudo mkdir -p /etc/containers/systemd
sudo cp deploy/gofin.container /etc/containers/systemd/gofin.container
sudo systemctl daemon-reload
sudo systemctl enable --now gofin.service
```

If your media disk is mounted at `/mnt/media`, edit `/etc/containers/systemd/gofin.container` and change:

```text
Volume=/mnt/media:/media:ro
```

## Direct systemd

Build and install the binary:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o gofin ./cmd/gofin
sudo install -m 0755 gofin /usr/local/bin/gofin
```

Create a service user and give it the app directory:

```sh
sudo useradd --system --home /srv/gofin --shell /usr/sbin/nologin gofin
sudo chown -R gofin:gofin /srv/gofin
```

Create the first GoFin administrator with the installed binary. This is the account that signs in from a Jellyfin-compatible client; it is separate from the Linux `gofin` service account.

```sh
printf '%s' 'choose-a-strong-password' | sudo -u gofin /usr/local/bin/gofin user add \
  --config /srv/gofin/gofin.json \
  --name admin \
  --password-stdin
```

Make sure the `gofin` user can read your media folders. Then install and start the service. It will scan the configured Movies and TV Shows paths on startup when `"on_start": true` is set in the config:

```sh
sudo cp deploy/gofin.service /etc/systemd/system/gofin.service
sudo systemctl daemon-reload
sudo systemctl enable --now gofin.service
```

Check it:

```sh
curl http://127.0.0.1:8096/System/Info/Public
systemctl status gofin.service
```

## Production notes

- Tags matching `v*` trigger GitHub Actions to publish static `linux/amd64` and `linux/arm64` binaries, plus a multi-architecture image to GitHub Container Registry.
- The container image has no Go compiler, shell, package manager, or Alpine runtime. It includes the static binary and CA certificates needed for TMDB HTTPS requests.
- Keep `TMDB_API_KEY` in `/srv/gofin/gofin.env`, not in the config file.
- For internet exposure, place GoFin behind a TLS reverse proxy. The server is intended for authenticated LAN use. See [Rate limiting](#rate-limiting) before you do.
- Keep the database on local storage. Media can live on a mounted disk.
- If your media mount is not `/srv/media`, update `RequiresMountsFor=` in the installed unit to match it.
- Back up `/srv/gofin/gofin.db`.
- Use direct play friendly files (`mkv`, `mp4`, `m4v`, `avi`, `mov`, `webm`).
- External links are already populated from TMDB/IMDb metadata when available.

## Rate limiting

`POST /Users/AuthenticateByName` allows **10 attempts per client IP per minute**. Attempts beyond that get `429 Too Many Requests` with a `Retry-After` header holding the seconds left in the window. The counter resets a minute after the first attempt in the window, so a locked-out client recovers on its own. Successful and failed logins both count.

The limiter tracks at most 1024 client IPs at a time. Once full it drops entries older than a minute; if every entry is still live it rejects new logins rather than growing without bound.

Only the login endpoint is limited. Every other endpoint requires a token, so it is already closed to anonymous callers.

**Behind a reverse proxy this limiter stops working as intended.** It keys on the connection's remote address, and it does not read `X-Forwarded-For`, so every request appears to come from the proxy. All clients then share one 10-per-minute bucket and a single attacker locks out the whole household. If you terminate TLS at a proxy, rate limit the login endpoint there instead, keyed on the real client IP:

```nginx
limit_req_zone $binary_remote_addr zone=gofin_login:10m rate=10r/m;

location /Users/AuthenticateByName {
    limit_req zone=gofin_login burst=5 nodelay;
    proxy_pass http://127.0.0.1:8096;
}
```
- Transcoding is intentionally not included. Adding it means FFmpeg, media probing, larger images, more CPU, and more Jellyfin API behavior.
