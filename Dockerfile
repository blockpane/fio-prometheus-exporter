FROM golang:latest AS builder

WORKDIR /app
COPY . .
RUN go mod download && go build -ldflags "-s -w" -o fio-prometheus-exporter cmd/fio-prometheus-exporter/main.go


FROM debian:latest AS stager

ENV DEBIAN_FRONTEND noninteractive
RUN apt-get update && apt-get -y upgrade && apt-get -y install ca-certificates
RUN groupadd -g 13856 exporter && useradd -u 13856 -m -d /exporter -g exporter -r exporter


FROM scratch

COPY --from=stager /etc/passwd /etc/passwd
COPY --from=stager /etc/group /etc/group
COPY --from=stager --chown=exporter:exporter /exporter /exporter
COPY --from=stager /usr/lib /usr/lib
COPY --from=stager /lib /lib
COPY --from=stager /lib64 /lib64
COPY --from=stager /etc/ssl /etc/ssl

COPY --from=builder /app/fio-prometheus-exporter /

EXPOSE 13856
WORKDIR /exporter
USER exporter

ENTRYPOINT ["/fio-prometheus-exporter"]