# Dev image: the vpn binary plus Xray. Build context is the repository root.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/vpn ./cmd/vpn

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip && rm -rf /var/lib/apt/lists/*
COPY scripts/fetch-xray.sh /tmp/fetch-xray.sh
RUN /tmp/fetch-xray.sh /usr/local/share/xray && ln -s /usr/local/share/xray/xray /usr/local/bin/xray
COPY --from=build /out/vpn /usr/local/bin/vpn
ENTRYPOINT ["vpn"]
