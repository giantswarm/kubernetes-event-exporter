FROM golang:1.23 AS builder

ARG VERSION
ENV PKG=github.com/giantswarm/kubernetes-event-exporter/v2/pkg/version

ADD . /app
WORKDIR /app
RUN CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build -ldflags="-s -w -X ${PKG}.Version=${VERSION}" -a -o /main .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder --chown=nonroot:nonroot /main /kubernetes-event-exporter

# https://github.com/GoogleContainerTools/distroless/blob/main/base/base.bzl#L8C1-L9C1
USER 65532

ENTRYPOINT ["/kubernetes-event-exporter"]
