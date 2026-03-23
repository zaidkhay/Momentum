# See ARCHITECTURE.md §3.1 — frontend is deployed to Vercel in production
# This Dockerfile is provided for parity; local dev uses: npm run dev

FROM node:20-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
