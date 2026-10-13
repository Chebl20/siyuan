FROM golang:1.26-alpine AS build

WORKDIR /src
COPY kernel/go.mod kernel/go.sum ./kernel/
WORKDIR /src/kernel
RUN go mod download
COPY kernel/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/multiuser

FROM alpine:3.22
COPY --from=build /gateway /usr/local/bin/siyuan-gateway
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/siyuan-gateway"]
