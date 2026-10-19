# ─── Build Stage ─────────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

# 依存キャッシュレイヤー (go.mod/go.sum が変わった時だけ再取得)
COPY go.mod go.sum ./
RUN go mod download

# ソースをコピーしてビルド
COPY . .

ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -ldflags="-s -w" -o tollgate cmd/server/main.go

# ─── Runtime Stage ───────────────────────────────────────────────────────────
FROM alpine:3.24

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl wget

COPY --from=builder /app/tollgate /app/tollgate

ENV PORT=8000

EXPOSE 8000

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost:8000/livez || exit 1

CMD ["/app/tollgate"]
