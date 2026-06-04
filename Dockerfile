# --- Estágio Base Rust: Cache de Dependências ---
FROM rust:1.82-slim-bookworm AS rust-env
ARG TARGETARCH
WORKDIR /build/indexer

COPY indexer/Cargo.toml ./
RUN mkdir src && echo "pub fn dummy() {}" > src/lib.rs && \
    mkdir -p src/bin && echo "fn main() {}" > src/bin/build_index.rs

RUN if [ "$TARGETARCH" = "amd64" ]; then \
    export RUSTFLAGS="-C target-cpu=haswell -C target-feature=+avx2,+fma,+f16c,+bmi2,+popcnt -C link-arg=-s"; \
    else \
    export RUSTFLAGS="-C link-arg=-s"; \
    fi && \
    cargo build --release && rm -rf src

# --- Estágio Builder Rust: Geração do Index ---
FROM rust-env AS rust-builder
ARG TARGETARCH
COPY resources/ ./resources/
COPY indexer/src ./src

RUN if [ "$TARGETARCH" = "amd64" ]; then \
    export RUSTFLAGS="-C target-cpu=haswell -C target-feature=+avx2,+fma,+f16c,+bmi2,+popcnt -C link-arg=-s"; \
    else \
    export RUSTFLAGS="-C link-arg=-s"; \
    fi && \
    touch src/bin/build_index.rs && cargo run --release --bin build_index && ls -lh dataset.bin

# --- Estágio PGO: Coleta de perfil ---
FROM golang:1.24-bookworm AS pgo-collector
WORKDIR /app
COPY api/go.mod ./
COPY api/cmd ./cmd
COPY api/engine ./engine

RUN go mod tidy && go mod download
COPY --from=rust-builder /build/indexer/dataset.bin ./

RUN CGO_ENABLED=0 GOOS=linux go build -o profiler ./cmd/profiler/ && \
    ./profiler && \
    ls -lh cpu.pprof

# --- Estágio Builder Go: API Pura ---
FROM golang:1.24-bookworm AS go-builder
WORKDIR /app

COPY api/go.mod ./
COPY api/main.go .
COPY api/engine ./engine

RUN go mod tidy && go mod download
COPY --from=pgo-collector /app/cpu.pprof ./default.pgo

RUN CGO_ENABLED=0 GOOS=linux GOAMD64=v3 go build -pgo=default.pgo -ldflags="-s -w" -o rinha-api .

# --- Estágio Final: Imagem de Produção Enxuta ---
FROM gcr.io/distroless/cc-debian12:latest
WORKDIR /app

COPY --from=go-builder /app/rinha-api .
COPY --from=rust-builder /build/indexer/dataset.bin ./
COPY resources/normalization.json ./resources/
COPY resources/mcc_risk.json ./resources/

ENV GOMAXPROCS=1
CMD ["./rinha-api"]
