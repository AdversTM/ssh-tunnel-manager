FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server iproute2 \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd

EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
