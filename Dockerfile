FROM mirror.gcr.io/golang:1.23-alpine AS build

ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/lunapassport ./cmd/lunapassport

FROM mirror.gcr.io/alpine:3.20

RUN apk add --no-cache su-exec \
    && addgroup -S passport \
    && adduser -S -G passport passport \
    && mkdir /data \
    && chown passport:passport /data

COPY --from=build /out/lunapassport /usr/local/bin/lunapassport
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh
VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
