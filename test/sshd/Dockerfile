FROM alpine:3.20

RUN apk add --no-cache openssh-server socat \
 && adduser -D -s /bin/sh pwuser \
 && echo 'pwuser:testpass' | chpasswd \
 && adduser -D -s /bin/sh keyuser \
 && passwd -u keyuser \
 && mkdir -p /home/keyuser/.ssh \
 && chown keyuser:keyuser /home/keyuser/.ssh \
 && chmod 700 /home/keyuser/.ssh

COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 22
ENTRYPOINT ["/entrypoint.sh"]
