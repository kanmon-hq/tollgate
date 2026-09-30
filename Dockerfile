# ─── Build Stage ─────────────────────────────────────────────────────────────
FROM golang:1.27.1-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

# 依存キャッシュレイヤー (go.mod/go.sum が変わった時だけ再取得)
COPY go.mod go.sum ./
RUN go mod download

# ソースをコピーしてテスト＆ビルド
COPY . .

# 依存解決とテスト、静的バイナリビルド
RUN go mod tidy && \
    CGO_ENABLED=0 go test -v ./... && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o tollgate cmd/server/main.go

# ─── Runtime Stage ───────────────────────────────────────────────────────────
FROM alpine:3.24

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl wget

COPY --from=builder /app/tollgate /app/tollgate

ENV PORT=8000
ENV DYNAMODB_ENDPOINT=http://dynamodb:8000
ENV AWS_REGION=ap-northeast-1
ENV DYNAMODB_TABLE_NAME=TollgateAPIKeys

EXPOSE 8000

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q --spider http://localhost:8000/livez || exit 1

CMD ["/app/tollgate"]
