FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
	-o /out/iceradar-server ./cmd/server

FROM alpine:3.20
RUN adduser -D -u 10001 ice && apk add --no-cache ca-certificates
USER ice
COPY --from=build /out/iceradar-server /usr/local/bin/iceradar-server
EXPOSE 8080 9101
ENTRYPOINT ["/usr/local/bin/iceradar-server"]
