# The release workflow creates one binary for each supported architecture.
FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETARCH
COPY --chown=nonroot:nonroot dist/gofin_linux_${TARGETARCH} /usr/local/bin/gofin

USER nonroot:nonroot
EXPOSE 8096
ENTRYPOINT ["/usr/local/bin/gofin"]
CMD ["serve", "--config", "/config/gofin.json"]
