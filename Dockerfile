FROM golang:1.13.4-stretch AS builder

ENV SPHINX_VERSION 3.1.1-612d99f
RUN wget https://sphinxsearch.com/files/sphinx-${SPHINX_VERSION}-linux-amd64.tar.gz -O /tmp/sphinxsearch.tar.gz \
    && mkdir -pv /opt/sphinx && cd /opt/sphinx && tar -xf /tmp/sphinxsearch.tar.gz \
    && rm /tmp/sphinxsearch.tar.gz

ENV DOCKERIZE_VERSION v0.6.1
RUN wget https://github.com/jwilder/dockerize/releases/download/$DOCKERIZE_VERSION/dockerize-linux-amd64-$DOCKERIZE_VERSION.tar.gz \
    && tar -C /usr/local/bin -xzvf dockerize-linux-amd64-$DOCKERIZE_VERSION.tar.gz \
    && rm dockerize-linux-amd64-$DOCKERIZE_VERSION.tar.gz

RUN mkdir -p /go/src/github.com/sandeepone/mysql-manticore
WORKDIR /go/src/github.com/sandeepone/mysql-manticore
COPY . .
RUN make

FROM debian:stretch-slim
RUN apt-get update && apt-get install -y default-libmysqlclient-dev rsync
COPY --from=builder /go/src/github.com/sandeepone/mysql-manticore/bin/mysql-manticore /usr/local/bin/
COPY --from=builder /usr/local/bin/dockerize /usr/local/bin/
COPY --from=builder /opt/sphinx/sphinx-3.1.1/bin/indexer /usr/local/bin/
COPY etc/river.toml /etc/mysql-manticore/river.toml
COPY etc/dict /etc/mysql-manticore/dict
RUN mkdir -p /var/river
VOLUME /var/river

CMD dockerize -wait tcp://mysql:3306 -timeout 120s /usr/local/bin/mysql-manticore -config /etc/mysql-manticore/river.toml -data-dir /var/river -my-addr mysql:3306 -sph-addr sphinx:9308
