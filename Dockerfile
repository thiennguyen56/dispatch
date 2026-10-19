FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
# SQL migrations are embedded in the binary and applied at startup.
RUN CGO_ENABLED=0 go build -o /dispatch ./cmd/dispatch

FROM alpine:3.20

RUN addgroup -S dispatch && adduser -S dispatch -G dispatch
WORKDIR /app
COPY --from=build /dispatch /usr/local/bin/dispatch

RUN mkdir -p /var/lib/dispatch \
    && chown dispatch:dispatch /var/lib/dispatch

USER dispatch
EXPOSE 8080

ENTRYPOINT ["dispatch"]
