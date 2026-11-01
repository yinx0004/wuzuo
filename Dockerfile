# Image built by GoReleaser from the prebuilt binary.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/wuzuo /usr/local/bin/wuzuo
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/wuzuo"]
