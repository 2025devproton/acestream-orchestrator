# Stage 1: Build React panel
FROM node:20-slim AS panel-builder
WORKDIR /build/panel-react
COPY app/static/panel-react/package*.json ./
RUN npm install --ignore-scripts
COPY app/static/panel-react/ ./
# This will output to /build/panel (one level up, as configured in vite.config.js)
RUN npm run build

# Stage 2: Build Python dependencies (use 3.11 to match Distroless)
FROM python:3.11-slim AS python-builder
WORKDIR /build

# Install build dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends gcc g++ git && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

COPY requirements.txt ./
RUN pip install --upgrade pip && \
    pip install --no-cache-dir --prefix=/install \
    --trusted-host pypi.org --trusted-host pypi.python.org --trusted-host files.pythonhosted.org \
    -r requirements.txt

# Stage 3: Collect binary dependencies (Redis, GPG)
FROM debian:12-slim AS dependency-builder
RUN apt-get update && \
    apt-get install -y --no-install-recommends redis-server redis-tools gnupg && \
    mkdir -p /bundle && \
    # Collect Redis
    cp --parents /usr/bin/redis-server /bundle/ && \
    cp --parents /usr/bin/redis-cli /bundle/ && \
    ldd /usr/bin/redis-server | grep "=> /" | awk '{print $3}' | xargs -I '{}' cp --parents '{}' /bundle/ && \
    # Collect GnuPG
    cp --parents /usr/bin/gpg /bundle/ && \
    cp --parents /usr/bin/gpgconf /bundle/ && \
    ldd /usr/bin/gpg | grep "=> /" | awk '{print $3}' | xargs -I '{}' cp --parents '{}' /bundle/ && \
    ldd /usr/bin/gpgconf | grep "=> /" | awk '{print $3}' | xargs -I '{}' cp --parents '{}' /bundle/ && \
    mkdir -p /bundle/usr/lib /bundle/usr/share && \
    cp -a /usr/lib/gnupg /bundle/usr/lib/ && \
    cp -a /usr/share/gnupg /bundle/usr/share/ && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Stage 4: Build Go unified binary (proxy + orchestrator + controlplane)
FROM golang:1.25 AS go-builder
WORKDIR /orchestrator
COPY app/orchestrator/ .
RUN go test ./... && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o acestream-unified ./cmd

# Stage 5: Final runtime image with Distroless
FROM gcr.io/distroless/python3-debian12:latest
WORKDIR /app
ENV PYTHONUNBUFFERED=1
ENV PYTHONPATH=/usr/local/lib/python3.11/site-packages
ENV GNUPGHOME=/tmp/.gnupg

# Copy Python dependencies from builder
COPY --from=python-builder /install /usr/local

# Copy application files
COPY app ./app

# Copy built React panel from panel-builder
COPY --from=panel-builder /build/panel ./app/static/panel

# Copy collected binary dependencies (Redis, GPG)
COPY --from=dependency-builder /bundle/ /

# Copy Go unified binary
COPY --from=go-builder /orchestrator/acestream-unified /usr/local/bin/acestream-unified

# Startup: Redis → acestream-unified (proxy :8000 + orchestrator :8083 + controlplane) → proton-sidecar (:9099)
# acestream-unified is the single Go binary for all planes.
# proton-sidecar is the only Python process; it handles Proton VPN server list updates.
COPY app/start.py /app/start.py

EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD ["/usr/bin/python3", "/app/app/readiness.py", "--liveness"]
CMD ["/app/start.py"]
