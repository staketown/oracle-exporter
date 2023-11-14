FROM golang:1.19 AS exporter

ENV GOBIN=/go/bin
ENV GOPATH=/go
ENV CGO_ENABLED=0
ENV GOOS=linux

WORKDIR /exporter
COPY *.go go.sum go.mod ./
RUN go build -o /oracle-exporter .

FROM debian:buster-slim

RUN apt-get update && apt-get upgrade && apt-get install -y curl
RUN useradd -ms /bin/bash exporter && chown -R exporter /usr

EXPOSE 9300

COPY --from=exporter oracle-exporter /usr/bin/oracle-exporter

USER exporter