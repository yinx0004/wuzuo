# Image built by GoReleaser from the prebuilt binary.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/yuanfang /usr/local/bin/yuanfang
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/yuanfang"]
