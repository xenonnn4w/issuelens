FROM golang:1.25-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /issue-lens .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S lens && adduser -S -G lens -u 10001 lens && mkdir -p /data /app/web/out && chown lens:lens /data
COPY --from=build /issue-lens /usr/local/bin/issue-lens
USER lens
WORKDIR /app/backend
EXPOSE 8080
CMD ["issue-lens"]
