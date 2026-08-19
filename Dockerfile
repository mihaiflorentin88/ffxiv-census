FROM alpine:latest

RUN apk add --no-cache bash ca-certificates tzdata postgresql-client

WORKDIR /app

ARG BINARY=dist/ffxiv-census-linux-arm64
COPY ${BINARY} /app/ffxiv-census

ENTRYPOINT ["/app/ffxiv-census"]
CMD ["server", "--start", "--port", "8080"]
