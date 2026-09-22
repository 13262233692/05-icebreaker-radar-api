FROM golang:1.26-alpine AS build
WORKDIR /src

RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/radar_sim ./cmd/radar_sim

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 radar
COPY --from=build /out/server /usr/local/bin/ice-radar-server
USER radar
EXPOSE 8080 9101
ENTRYPOINT ["/usr/local/bin/ice-radar-server"]
