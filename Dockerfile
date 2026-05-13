FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/emberbox-agent ./cmd/emberbox-agent

FROM alpine:3.20
RUN apk add --no-cache bash ca-certificates
COPY --from=build /out/emberbox-agent /usr/local/bin/emberbox-agent
WORKDIR /work
EXPOSE 10000
ENTRYPOINT ["/usr/local/bin/emberbox-agent", "--listen", ":10000", "--workdir", "/work"]
