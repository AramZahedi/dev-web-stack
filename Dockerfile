# syntax=docker/dockerfile:1

FROM fedora:44 AS build

ARG VERSION=0.1.0-docker
ARG COMMIT=container
ARG PUBLIC_HTTP_PORT=8080
ARG PUBLIC_HTTPS_PORT=8443

RUN dnf -y install \
      ca-certificates \
      gzip \
      golang \
      make \
      nodejs \
      npm \
      tar \
    && dnf clean all

WORKDIR /src
COPY . .

RUN VITE_PUBLIC_HTTP_PORT="$PUBLIC_HTTP_PORT" \
    VITE_PUBLIC_HTTPS_PORT="$PUBLIC_HTTPS_PORT" \
    make build VERSION="$VERSION" COMMIT="$COMMIT"

FROM fedora:44

ARG DEVSTACK_UID=1000
ARG DEVSTACK_GID=1000
ARG PUBLIC_HTTP_PORT=8080
ARG PUBLIC_HTTPS_PORT=8443

RUN dnf -y install \
      ca-certificates \
      curl \
      findutils \
      gzip \
      nss-tools \
      p11-kit \
      procps-ng \
      shadow-utils \
      socat \
      tar \
      tini \
      util-linux \
    && dnf clean all

RUN groupadd --gid "${DEVSTACK_GID}" devstack \
    && useradd --uid "${DEVSTACK_UID}" --gid devstack --create-home \
       --home-dir /home/devstack --shell /bin/bash devstack \
    && install -d -o devstack -g devstack -m 0755 \
       /home/devstack/.devstack /workspace \
    && install -d -m 0755 /etc/devstack /var/lib/devstack-helper /run/devstack-helper

COPY --from=build /src/dist/devstack /usr/local/bin/devstack
COPY --from=build /src/dist/devstack-helper /usr/local/bin/devstack-helper
COPY docker/entrypoint.sh /usr/local/bin/devstack-entrypoint
COPY install.sh /usr/local/share/devstack/install.sh
COPY README.md LICENSE /usr/local/share/devstack/

RUN chmod 0755 /usr/local/bin/devstack /usr/local/bin/devstack-helper /usr/local/bin/devstack-entrypoint

ENV DEVSTACK_HOME=/home/devstack/.devstack \
    DEVSTACK_CONTAINER_HTTP_PORT=80 \
    DEVSTACK_CONTAINER_HTTPS_PORT=443 \
    DEVSTACK_CONTAINER_UI_PORT=7070 \
    DEVSTACK_PUBLIC_HTTP_PORT=${PUBLIC_HTTP_PORT} \
    DEVSTACK_PUBLIC_HTTPS_PORT=${PUBLIC_HTTPS_PORT} \
    DEVSTACK_PROXY_HTTP_PORT=8080 \
    DEVSTACK_PROXY_HTTPS_PORT=8443 \
    DEVSTACK_UI_INTERNAL_PORT=7071 \
    DEVSTACK_HELPER_HTTP_PORT=18080 \
    DEVSTACK_HELPER_HTTPS_PORT=18443 \
    DEVSTACK_HOST_HTTP_PORT=${PUBLIC_HTTP_PORT} \
    DEVSTACK_HOST_HTTPS_PORT=${PUBLIC_HTTPS_PORT} \
    DEVSTACK_HOST_UI_PORT=7070 \
    DEVSTACK_UID=${DEVSTACK_UID} \
    DEVSTACK_GID=${DEVSTACK_GID} \
    DEVSTACK_CONFIGURE=1 \
    DEVSTACK_INIT=1

EXPOSE 80 443 7070

HEALTHCHECK --interval=15s --timeout=5s --start-period=45s --retries=6 \
  CMD curl -fsS "http://127.0.0.1:${DEVSTACK_CONTAINER_UI_PORT}/api/v1/health" || exit 1

STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/devstack-entrypoint"]
