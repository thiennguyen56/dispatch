FROM golang:1.22-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 go build -o /dispatch ./cmd/dispatch

FROM alpine:3.20

RUN addgroup -S dispatch && adduser -S dispatch -G dispatch
WORKDIR /app
COPY --from=build /dispatch /usr/local/bin/dispatch

USER dispatch
EXPOSE 8080

ENTRYPOINT ["dispatch"]
