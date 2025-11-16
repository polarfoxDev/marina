FROM alpine:3.20

# Copy the backup script
COPY backup.sh /backup.sh
RUN chmod +x /backup.sh

# The backup script will be executed by Marina
ENTRYPOINT ["/backup.sh"]
