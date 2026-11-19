# Use `just filestash build-plugins` to verify each ZIP before Docker copies it.
FROM busybox:1.37.0@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
COPY vendor/*.zip /plugins/
USER 1000:1000
