# syntax=docker/dockerfile:1

# ---- build ---------------------------------------------------------------
FROM golang:1.26 AS build
WORKDIR /src

# Cache modules separately from source for faster rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/latprobe ./cmd/latprobe

# ---- runtime -------------------------------------------------------------
# Static binary on distroless: no shell, no package manager, non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/latprobe /usr/local/bin/latprobe
COPY examples/config.yaml /etc/latprobe/config.yaml
EXPOSE 9310
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/latprobe"]
CMD ["serve", "-c", "/etc/latprobe/config.yaml", "--listen", ":9310"]
