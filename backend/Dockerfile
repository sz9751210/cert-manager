# 階段 1: 建置環境 (Builder)
FROM golang:1.25-alpine AS builder

# 安裝 git (go mod download 可能需要)
RUN apk add --no-cache git

WORKDIR /app

# 先複製依賴檔，利用 Docker Layer 快取機制加速
COPY go.mod go.sum ./
RUN go mod download

# 複製程式碼
COPY . .

# 編譯 (CGO_ENABLED=0 確保編譯出純靜態二進制檔)
RUN CGO_ENABLED=0 GOOS=linux go build -o cert-manager cmd/api/main.go

# 階段 2: 執行環境 (Runner)
FROM alpine:latest

# 安裝 CA 憑證 (重要！否則無法進行 SSL 掃描和呼叫 Cloudflare API)
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

# 從 Builder 階段複製編譯好的執行檔
COPY --from=builder /app/cert-manager .

# 複製設定檔目錄 (如果有預設設定)
# 注意：實際部署時通常會用 Volume 掛載覆蓋
COPY --from=builder /app/config ./config

# 暴露 Port
EXPOSE 8080

# 啟動
CMD ["./cert-manager"]
