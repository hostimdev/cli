# Built by GoReleaser (dockers_v2) from the release binaries, not by hand:
# the build context holds linux/<arch>/hostim for each platform.
# distroless/static carries the CA certificates the CLI needs for HTTPS.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
LABEL io.modelcontextprotocol.server.name="io.github.hostimdev/hostim"
COPY $TARGETPLATFORM/hostim /hostim
ENTRYPOINT ["/hostim"]
