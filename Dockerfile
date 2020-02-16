############################
# STEP 1 build executable binary
############################
FROM golang:1.13 AS builder

# ENV SPHINX_VERSION 3.1.1-612d99f
# RUN wget https://sphinxsearch.com/files/sphinx-${SPHINX_VERSION}-linux-amd64.tar.gz -O /tmp/sphinxsearch.tar.gz \
#     && mkdir -pv /opt/sphinx && cd /opt/sphinx && tar -xf /tmp/sphinxsearch.tar.gz \
#     && rm /tmp/sphinxsearch.tar.gz

# ENV DOCKERIZE_VERSION v0.6.1
# RUN wget https://github.com/jwilder/dockerize/releases/download/$DOCKERIZE_VERSION/dockerize-linux-amd64-$DOCKERIZE_VERSION.tar.gz \
#     && tar -C /usr/local/bin -xzvf dockerize-linux-amd64-$DOCKERIZE_VERSION.tar.gz \
#     && rm dockerize-linux-amd64-$DOCKERIZE_VERSION.tar.gz

ADD . /go/src/github.com/sandeepone/mysql-manticore

RUN cd /go/src/github.com/sandeepone/mysql-manticore \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w  \
    -X main.version=0.1" \
    -a -tags netgo -installsuffix netgo -o mysql-manticore cmd/mysql-manticore/main.go

############################
# STEP 2 build a release image
############################
FROM alpine:3.11

RUN apk add --no-cache curl bash rsync mariadb-client

COPY --from=builder /go/src/github.com/sandeepone/mysql-manticore/mysql-manticore /usr/local/bin/
# COPY --from=builder /usr/local/bin/dockerize /usr/local/bin/
# COPY --from=builder /opt/sphinx/sphinx-3.1.1/bin/indexer /usr/local/bin/
COPY etc/river.toml /etc/mysql-manticore/river.toml
COPY etc/dict /etc/mysql-manticore/dict

RUN mkdir -p /var/river
VOLUME /var/river

EXPOSE 8080

# CMD dockerize -wait tcp://mysql:3306 -timeout 120s /usr/local/bin/mysql-manticore -config /etc/mysql-manticore/river.toml -data-dir /var/river -my-addr mysql:3306 -sph-addr sphinx:9308

ENTRYPOINT ["mysql-manticore", "-config", "/etc/mysql-manticore/river.toml"]
