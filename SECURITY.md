# Security

## Reporting a vulnerability

Report vulnerabilities privately through [GitHub security advisories](https://github.com/bharathajjarapu/GoFin/security/advisories/new). Do not open a public issue. Only the latest version gets security fixes.

## What GoFin protects

- GoFin stores passwords as bcrypt hashes. A login for an unknown user still runs a bcrypt comparison, so response time does not reveal which names exist.
- Access tokens are 256 random bits. The database stores only their SHA-256 hash.
- Every endpoint except server discovery and login requires a token.
- Login allows 10 attempts per client IP per minute. See [rate limiting](DEPLOY.md#rate-limiting).
- Child accounts see only media at or below their rating, including inside playlists, images and streams. A child account is never an administrator.
- The request log records the method and path, never the query string, so tokens passed as `api_key` stay out of it.
- GoFin rejects JSON request bodies over 64 KB.
- Image redirects go only to `image.tmdb.org`.
- GoFin writes the config file with mode `0600`. Keep `TMDB_API_KEY` in the environment, not the config.

## Known limits

- GoFin has no TLS. Put it behind a TLS reverse proxy before exposing it to the internet.
- Tokens never expire. Logging out revokes that one token, and no command revokes the rest.
- Clients may send the token in the URL as `api_key`, which proxies and browsers can log.
- Any signed-in user can edit or delete the shared playlists.
- Item responses include the file's path on the server.
