# Plugin bundle for Filestash. Build only through `just filestash build-plugins`:
# the recipe downloads and checks every vendor file into the build context
# (vendor/*.zip, lowa/*) before this file runs. No stage downloads anything
# except the pinned Filestash source.

# Stage 1: build application_office.zip from the Filestash source.
FROM alpine:3.22.2@sha256:4b7ce07002c69e8f3d704a9c5d6fd3053be500b7f1c69fc0d80990c2ad8dd412 AS office
ARG FILESTASH_REF
RUN apk add --no-cache git clang lld zip
WORKDIR /src
RUN git init -q filestash && cd filestash \
    && git remote add origin https://github.com/mickael-kerjean/filestash \
    && git fetch -q --depth 1 origin "${FILESTASH_REF}" \
    && git checkout -q FETCH_HEAD
WORKDIR /src/filestash/server/plugin/plg_application_office
COPY lowa/ lib/lowa/
RUN clang --target=wasm32 -nostdlib -O3 -Wl,--no-entry -Wl,--export=middleware \
        -o middleware.wasm middleware.c \
    && mkdir -p /out \
    && zip -qr /out/application_office.zip . -x Makefile -x middleware.c

# Stage 2: the bundle. Only ZIP files under /plugins.
FROM busybox:1.37.0@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
COPY vendor/*.zip /plugins/
COPY --from=office /out/application_office.zip /plugins/
USER 1000:1000
