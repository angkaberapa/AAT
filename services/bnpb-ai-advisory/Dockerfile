FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .
# Folder state milik user nonroot; distroless tidak punya mkdir/chown, dan volume bernama
# yang di-mount ke /data mewarisi pemilik folder ini.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
ENV PORT=8000
COPY --from=build /out/app /app
COPY --from=build --chown=nonroot:nonroot /out/data /data
EXPOSE 8000
USER nonroot
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/app", "healthcheck"]
ENTRYPOINT ["/app"]
