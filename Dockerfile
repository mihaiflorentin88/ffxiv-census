FROM alpine:latest

RUN apk add --no-cache bash ca-certificates tzdata postgresql-client

WORKDIR /app

COPY dist/ffxiv-census-linux-arm64 /app/ffxiv-census

ENTRYPOINT ["/app/ffxiv-census"]
CMD ["server", "--start", "--port", "8080"]
