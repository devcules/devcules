# M0.5 runtime image. Base images pin by digest with the tag in a
# comment; the docker Dependabot ecosystem keeps the digests fresh.
# Agent CLIs are never baked in (PLAN.md §1.3); there is no web stage
# yet (web/ lands in M1.2).
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
  go build -trimpath \
  -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" \
  -o /out/devcules ./cmd/devcules

FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8
RUN apt-get update && apt-get install -y --no-install-recommends \
  tmux git bash ca-certificates \
  && rm -rf /var/lib/apt/lists/* \
  && useradd --uid 10001 --create-home --shell /bin/bash devcules \
  && mkdir -p /data /workspace \
  && chown devcules:devcules /data /workspace
COPY --from=build /out/devcules /usr/local/bin/devcules
USER devcules
# Set explicitly: cgroup v2 hides the container from auto-detection.
ENV DEVCULES_DATA_DIR=/data
EXPOSE 7659
ENTRYPOINT ["devcules"]
CMD ["serve"]
# No HEALTHCHECK here: it would apply to the runner too, which serves nothing.
# deploy/compose.yaml declares the check on serve only.
