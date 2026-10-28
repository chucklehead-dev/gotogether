# gotogether's pinned image (ROADMAP I21, docs/ci.md): Go, Node, quint and
# its Rust evaluator at pinned versions, and the gotogether CLI built from
# the build context (the repository's root):
#
#   docker build -f ci/image/Dockerfile -t gotogether .
#   docker run --rm -v "$PWD:/work" -w /work gotogether gotogether validate -binding b.yaml runs/*.jsonl
#
# CI builds it and runs gotogether doctor -strict inside. It is published
# nowhere: which registry, and under what name, is the maintainer's call.
# Change a version here and in the Action's defaults together.
ARG GO_VERSION=1.26.1
FROM golang:${GO_VERSION}-bookworm

ARG NODE_VERSION=22.22.0
ARG QUINT_VERSION=0.33.0

# Node from nodejs.org's release (gzip: the image has no xz), checked against its SHASUMS256
RUN set -eux; \
    case "$(dpkg --print-architecture)" in \
      amd64) arch=x64 ;; arm64) arch=arm64 ;; \
      *) echo "no Node build for $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac; \
    base="https://nodejs.org/dist/v${NODE_VERSION}"; \
    file="node-v${NODE_VERSION}-linux-${arch}.tar.gz"; \
    curl -fsSLO "$base/$file"; \
    curl -fsSL "$base/SHASUMS256.txt" | grep " $file\$" | sha256sum -c -; \
    tar -xzf "$file" -C /usr/local --strip-components=1 --no-same-owner; \
    rm "$file"; \
    node --version

# quint and the evaluator it expects, from its release asset (not
# GitHub's rate-limited API), where any user finds it
ENV QUINT_HOME=/opt/quint
COPY ci/install-quint.sh /usr/local/bin/install-quint.sh
RUN QUINT_VERSION=${QUINT_VERSION} install-quint.sh && chmod -R a+rX /opt/quint

# the CLI, from the context's source
WORKDIR /src/gotogether
COPY . .
RUN GOWORK=off CGO_ENABLED=0 go install ./cmd/gotogether && rm -rf /src/gotogether && go clean -cache

WORKDIR /work
CMD ["gotogether", "doctor"]
