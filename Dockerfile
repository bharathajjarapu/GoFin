FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gofin ./cmd/gofin

FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
	&& addgroup -S -g 10001 gofin \
	&& adduser -S -D -H -u 10001 -G gofin gofin \
	&& mkdir -p /config /media \
	&& chown -R gofin:gofin /config

COPY --from=build /out/gofin /usr/local/bin/gofin

USER gofin
EXPOSE 8096
VOLUME ["/config", "/media"]
ENTRYPOINT ["gofin"]
CMD ["serve", "--config", "/config/gofin.json"]
