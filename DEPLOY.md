# Deploy GoFin

Run GoFin as a plain systemd service, or in a Podman container. Systemd has fewer parts. Podman isolates the server and moves to another machine more easily. Release images already contain the binary, so you need Go only to build it yourself.

## Build and run

GoFin needs Go 1.26 or newer. On Debian or Ubuntu:

```sh
sudo apt update
sudo apt install -y golang-go
go version
```

If that version is older than 1.26, install the official archive for your CPU:

```sh
GO_ARCHIVE=go1.26.5.linux-amd64.tar.gz
wget "https://go.dev/dl/$GO_ARCHIVE"
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "$GO_ARCHIVE"
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.profile
. ~/.profile
go version
```

Build the binary and create a config. `config init` writes a unique server ID. Keep it when you edit the file.

```sh
go build -o gofin ./cmd/gofin
mkdir -p media/movies media/tv
./gofin config init
```

Set the library paths in `gofin.json` as shown in the [configuration reference](README.md#configuration), and name files as in [Naming](README.md#naming). Then add an administrator, scan and serve:

```sh
export TMDB_API_KEY="your-tmdb-key"
./gofin user add --config gofin.json --name admin --password 'choose-a-strong-password'
./gofin scan --config gofin.json
./gofin serve --config gofin.json
```

Open `http://127.0.0.1:8096`, or `http://SERVER-IP:8096` from another device.

## Server layout

The examples below use these paths:

```text
/srv/gofin/gofin.json   # config
/srv/gofin/gofin.db     # SQLite database
/srv/gofin/gofin.env    # secrets
/srv/gofin/covers/      # cover art copied out of music files
/srv/media/movies       # movies
/srv/media/tv           # TV shows
```

If your media disk is mounted elsewhere, such as `/mnt/media`, use that path for systemd, or mount `/mnt/media:/media:ro` for Podman.

Create the directories and the secrets file:

```sh
sudo mkdir -p /srv/gofin /srv/media/movies /srv/media/tv
sudo tee /srv/gofin/gofin.env >/dev/null <<'EOF'
TMDB_API_KEY=your-tmdb-key
EOF
sudo chmod 600 /srv/gofin/gofin.env
```

Config for Podman:

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

For systemd, change these fields:

```json
"database": { "path": "/srv/gofin/gofin.db" },
"libraries": [
  { "name": "Movies", "type": "movies", "path": "/srv/media/movies" },
  { "name": "TV Shows", "type": "tvshows", "path": "/srv/media/tv" }
]
```

Save the config as `/srv/gofin/gofin.json`.

## Podman

The image copies a static binary from `dist/gofin_linux_<arch>` into a distroless base. To build it locally:

```sh
arch=$(go env GOARCH)
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w" \
  -o "dist/gofin_linux_$arch" ./cmd/gofin
sudo podman build --build-arg TARGETARCH="$arch" -t gofin:local .
```

To use a release instead, replace `gofin:local` below with `ghcr.io/bharathajjarapu/gofin:VERSION`. Releases cover `linux/amd64` and `linux/arm64`.

Run it once:

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

Or install it as a service with Quadlet:

```sh
sudo chown -R 65532:65532 /srv/gofin
sudo mkdir -p /etc/containers/systemd
sudo cp deploy/gofin.container /etc/containers/systemd/gofin.container
sudo systemctl daemon-reload
sudo systemctl enable --now gofin.service
```

If your media is not in `/srv/media`, change the `Volume=` line in `/etc/containers/systemd/gofin.container`, for example to `Volume=/mnt/media:/media:ro`.

## Systemd

Install the binary and create a service account that owns `/srv/gofin`:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o gofin ./cmd/gofin
sudo install -m 0755 gofin /usr/local/bin/gofin
sudo useradd --system --home /srv/gofin --shell /usr/sbin/nologin gofin
sudo chown -R gofin:gofin /srv/gofin
```

Add the first GoFin administrator. Clients sign in with this account. It is separate from the `gofin` Linux account.

```sh
printf '%s' 'choose-a-strong-password' | sudo -u gofin /usr/local/bin/gofin user add \
  --config /srv/gofin/gofin.json \
  --name admin \
  --password-stdin
```

Give the `gofin` user read access to your media, then start the service. With `"on_start": true` it scans on startup.

```sh
sudo cp deploy/gofin.service /etc/systemd/system/gofin.service
sudo systemctl daemon-reload
sudo systemctl enable --now gofin.service
curl http://127.0.0.1:8096/System/Info/Public
```

## Production notes

- Pushing a `v*` tag makes GitHub Actions publish `linux/amd64` and `linux/arm64` binaries and a multi-architecture image to GitHub Container Registry.
- The image holds only the static binary and the CA certificates TMDB needs. It has no shell or package manager.
- Keep `TMDB_API_KEY` in `/srv/gofin/gofin.env`, not in the config.
- GoFin has no TLS. Put it behind a TLS reverse proxy before exposing it to the internet, and read [Rate limiting](#rate-limiting) first. See [SECURITY.md](SECURITY.md) for the rest.
- Keep the database on local storage. Media can live on a mounted disk.
- If your media is not in `/srv/media`, update `RequiresMountsFor=` in the installed unit.
- Stop the server, then back up `gofin.db` together with `gofin.db-wal`. The `-wal` file holds recent changes until SQLite merges them. `covers/` needs no backup, because a rescan rebuilds it.
- Use files clients can play directly, such as `mkv`, `mp4`, `m4v`, `avi`, `mov` or `webm`. GoFin does not transcode.

## Rate limiting

`POST /Users/AuthenticateByName` allows 10 attempts per client IP per minute, counting both successes and failures. Further attempts get `429 Too Many Requests` with a `Retry-After` header. The count resets a minute after the first attempt. GoFin tracks at most 1024 IPs, and when every one of them is still active it rejects new logins.

Behind a reverse proxy, every request comes from the proxy's IP, because GoFin does not read `X-Forwarded-For`. All clients then share one limit, and one attacker can lock out the household. Rate limit the login at the proxy instead:

```nginx
limit_req_zone $binary_remote_addr zone=gofin_login:10m rate=10r/m;

location /Users/AuthenticateByName {
    limit_req zone=gofin_login burst=5 nodelay;
    proxy_pass http://127.0.0.1:8096;
}
```
