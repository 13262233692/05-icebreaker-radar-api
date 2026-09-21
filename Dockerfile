# Build stage
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/simulator ./cmd/simulator

# Runtime stage
FROM alpine:3.20
RUN adduser -D -u 10001 ice && apk add --no-cache ca-certificates
COPY --from=build /out/server /usr/local/bin/iceradar-server
COPY --from=build /out/simulator /usr/local/bin/iceradar-simulator
USER ice
EXPOSE 8080 9101
ENTRYPOINT ["/usr/local/bin/iceradar-server"]
