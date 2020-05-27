############################
# STEP 1 build executable binary
############################
FROM golang:1.14 AS builder

ADD . /go/src/github.com/sandeepone/mysql-manticore

RUN cd /go/src/github.com/sandeepone/mysql-manticore \
 && COMMIT_SHA=$(git rev-parse --short HEAD) \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w  \
    -X main.version=0.8.0 \
    -X main.revision=${COMMIT_SHA}" \
    -a -tags netgo -installsuffix netgo -o mysql-manticore cmd/mysql-manticore/main.go

############################
# STEP 2 build a certs image
############################

# Alpine certs
FROM alpine:3.11 as alpine

RUN apk update && apk add --no-cache ca-certificates tzdata && update-ca-certificates

# Create appuser
ENV USER=appuser
ENV UID=10001

# See https://stackoverflow.com/a/55757473/12429735
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid "${UID}" \
    "${USER}"

############################
# STEP 3 build a release image
############################
FROM scratch
MAINTAINER Sandeep Sangamreddi <sandeepone@gmail.com>

# Import from builder.
COPY --from=alpine /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=alpine /etc/passwd /etc/passwd
COPY --from=alpine /etc/group /etc/group

COPY --from=builder /go/src/github.com/sandeepone/mysql-manticore/mysql-manticore /usr/bin/
COPY etc/river.toml /etc/mysql-manticore/river.toml
COPY etc/dict /etc/mysql-manticore/dict

EXPOSE 8080

# Use an unprivileged user.
USER appuser:appuser

ENTRYPOINT ["mysql-manticore", "-config", "/etc/mysql-manticore/river.toml"]