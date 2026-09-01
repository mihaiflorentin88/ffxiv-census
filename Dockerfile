FROM alpine:latest

RUN apk add --no-cache bash ca-certificates tzdata postgresql-client

WORKDIR /app

ARG TARGETARCH
COPY dist/ffxiv-census-linux-${TARGETARCH} /app/ffxiv-census

ENTRYPOINT ["/app/ffxiv-census"]
CMD ["server", "--start", "--port", "8080"]
