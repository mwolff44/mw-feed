# SPDX-License-Identifier: GPL-3.0-or-later

FROM golang:1.27 AS build
WORKDIR /src
COPY src/go.mod ./
RUN go mod download
COPY src/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/mw-feed ./cmd/mw-feed

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/mw-feed /mw-feed
USER nonroot:nonroot
ENTRYPOINT ["/mw-feed"]
