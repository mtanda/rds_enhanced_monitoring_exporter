FROM        quay.io/prometheus/busybox:glibc
MAINTAINER  Mitsuhiro Tanda <mitsuhiro.tanda@gmail.com>

COPY rds_enhanced_monitoring_exporter /bin/rds_enhanced_monitoring_exporter

EXPOSE      9100
USER        nobody
ENTRYPOINT  [ "/bin/rds_enhanced_monitoring_exporter" ]
