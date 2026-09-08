FROM rust:1.82-slim AS builder
WORKDIR /app
COPY Cargo.toml Cargo.lock* ./
COPY src ./src
COPY circuits ./circuits
RUN cargo build --release

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/target/release/zk-service /zk-service
COPY --from=builder /app/circuits /circuits
EXPOSE 8081
ENTRYPOINT ["/zk-service"]
