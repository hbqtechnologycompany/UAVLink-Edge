# Bootstrap image — git pull + trigger host install (ARM64 / AMD64)
FROM debian:bookworm-slim

RUN apt-get update -qq \
    && apt-get install -y -qq --no-install-recommends \
        git ca-certificates util-linux bash \
    && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
