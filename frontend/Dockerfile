# 階段 1: 建置 React
FROM node:20-alpine as build-stage
WORKDIR /app
COPY package*.json ./
RUN npm install
COPY . .
RUN npm run build

# 階段 2: Nginx 伺服器
FROM nginx:alpine as production-stage
# 複製編譯好的 dist 資料夾到 Nginx 目錄
COPY --from=build-stage /app/dist /usr/share/nginx/html
# 複製自定義的 nginx 設定
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]