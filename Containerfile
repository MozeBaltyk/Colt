# ─────────────────────────────────────────────────────────────────────────────
#  Containerfile — template for building an execution environment.
#
#  Purpose:  Single container image to run justfile as if you were on your laptop.
#
#  Build:    just build
#
#  Base:     Ubuntu 24.04 LTS (Noble) — matches standard WSL.
# ─────────────────────────────────────────────────────────────────────────────

FROM ubuntu:24.04 AS builder

LABEL org.opencontainers.image.title="${PROJECT_NAME:-toolkit}"
LABEL org.opencontainers.image.description="Bundles all CLI tools needed for this project's deployment"
LABEL org.opencontainers.image.version="${VERSION:-0.0.0}"

# ── Prevent apt from prompting for configuration ─────────────────────────────
ENV DEBIAN_FRONTEND=noninteractive

# ── Tool versions (pinned for reproducibility) ───────────────────────────────
ARG KUBECTL_VERSION=v1.36.0
ARG HELM_VERSION=v3.17.2
ARG HELMFILE_VERSION=v0.171.0
ARG YQ_VERSION=v4.45.1
ARG JUST_VERSION=1.58.0

# ── Install system dependencies ─────────────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    gnupg \
    lsb-release \
    git \
    jq \
    postgresql-client \
    redis-tools \
    dnsutils \
    openssh-client \
    ansible \
    sshpass \
    tar \
    gzip \
    xz-utils \
    unzip \
    && rm -rf /var/lib/apt/lists/*

# ── Install kubectl ──────────────────────────────────────────────────────────
RUN curl -fsSL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
    -o /usr/local/bin/kubectl \
    && chmod +x /usr/local/bin/kubectl

# ── Install Helm ─────────────────────────────────────────────────────────────
RUN curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" \
    | tar -xz --strip-components=1 -C /usr/local/bin linux-amd64/helm

# ── Install Helmfile ─────────────────────────────────────────────────────────
RUN curl -fsSL "https://github.com/helmfile/helmfile/releases/download/${HELMFILE_VERSION}/helmfile_${HELMFILE_VERSION#v}_linux_amd64.tar.gz" \
    | tar -xz -C /usr/local/bin helmfile \
    && chmod +x /usr/local/bin/helmfile

# ── Install yq ───────────────────────────────────────────────────────────────
RUN curl -fsSL "https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_amd64" \
    -o /usr/local/bin/yq \
    && chmod +x /usr/local/bin/yq

# ── Install just ─────────────────────────────────────────────────────────────
RUN curl -fsSL "https://github.com/casey/just/releases/download/${JUST_VERSION}/just-${JUST_VERSION}-x86_64-unknown-linux-musl.tar.gz" \
    | tar -xz -C /usr/local/bin just \
    && chmod +x /usr/local/bin/just

# ── Install Skopeo (via Ubuntu repo for compatibility) ───────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends skopeo \
    && rm -rf /var/lib/apt/lists/*


# ── Verify all tools are installed (best-effort, won't fail the build) ────────
RUN echo "=== Installed tool versions ===" && \
    (kubectl version --client --output=yaml 2>/dev/null | grep -oP 'gitVersion: \K.*' || echo "kubectl:   FAILED") && \
    (helm version --short 2>/dev/null          || echo "helm:      FAILED") && \
    (helmfile --version 2>/dev/null            || echo "helmfile:  FAILED") && \
    (yq --version 2>/dev/null                  || echo "yq:        FAILED") && \
    (jq --version 2>/dev/null                  || echo "jq:        FAILED") && \
    (just --version 2>/dev/null                || echo "just:      FAILED") && \
    (skopeo --version 2>/dev/null              || echo "skopeo:    FAILED") && \
    (ansible --version 2>/dev/null | head -1   || echo "ansible:   FAILED") && \
    (sshpass -V 2>&1 | head -1                 || echo "sshpass:   FAILED") && \
    (git --version 2>/dev/null                 || echo "git:       FAILED") && \
    (psql --version 2>/dev/null                || echo "psql:      FAILED") && \
    (redis-cli --version 2>/dev/null           || echo "redis-cli: FAILED") && \
    (dig -v 2>&1 | head -1                     || echo "dig:       FAILED") && \
    echo "=== Verification complete ==="

# ── Default command ──────────────────────────────────────────────────────────
CMD ["/bin/bash"]
