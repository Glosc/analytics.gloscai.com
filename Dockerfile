FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS server
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
ARG GOPROXY=https://proxy.golang.org,direct
RUN go mod download
COPY backend/ ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/aptabase .

FROM --platform=$BUILDPLATFORM node:22 AS webapp
WORKDIR /work
COPY src/package.json src/package-lock.json ./
RUN npm ci
COPY src/ ./
RUN npm run build

FROM scratch
WORKDIR /app
ENV HOME=/home/app
COPY --from=server /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=server /out/aptabase ./aptabase
COPY --from=webapp /work/wwwroot ./wwwroot
COPY etc/clickhouse ./etc/clickhouse
COPY etc/geoip ./etc/geoip
COPY LICENSE .
ENTRYPOINT ["/app/aptabase"]
