FROM golang:1.19 AS exporter

ENV GOBIN=/go/bin
ENV GOPATH=/go
ENV CGO_ENABLED=0
ENV GOOS=linux

RUN git clone "https://github.com/staketown/oracle-exporter.git" /exporter
WORKDIR /exporter
RUN go install

FROM debian:buster-slim

RUN apt-get update && apt-get upgrade && apt-get install -y curl
RUN useradd -ms /bin/bash exporter && chown -R exporter /usr

EXPOSE 9300

COPY --from=exporter /go/bin/main /usr/bin/oracle-exporter

USER exporter

ENTRYPOINT ["/usr/bin/oracle-exporter"]

CMD ["--node localhost:9090"]