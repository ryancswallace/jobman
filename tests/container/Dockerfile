ARG BASE_IMAGE=jobman:local
FROM ${BASE_IMAGE}

USER root
COPY --chown=10001:10001 --chmod=0555 target.sh /opt/jobman/bin/container-target
USER 10001:10001
