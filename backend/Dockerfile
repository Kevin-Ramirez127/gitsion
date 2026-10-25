# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gitsion ./cmd/server

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 app

COPY --from=build /out/gitsion /usr/local/bin/gitsion

USER app
EXPOSE 8080
ENTRYPOINT ["gitsion"]
