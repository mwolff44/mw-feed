# SPDX-License-Identifier: GPL-3.0-or-later

FROM golang:1.27 AS build
WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/mw-feed ./cmd/mw-feed
RUN mkdir /out/data

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/mw-feed /mw-feed
COPY LICENSE NOTICE THIRD_PARTY_LICENSES.md /licenses/
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV MW_FEED_DATA=/data
VOLUME /data
USER nonroot:nonroot
EXPOSE 8080
# The image has no shell: the binary checks itself.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/mw-feed", "healthcheck"]
ENTRYPOINT ["/mw-feed"]
CMD ["serve"]
