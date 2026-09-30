ARG GO_BUILDER_IMAGE=golang:1.27.1-bookworm
FROM --platform=$BUILDPLATFORM ${GO_BUILDER_IMAGE} AS build

ARG TARGETOS
ARG TARGETARCH
ENV GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -buildvcs=false -ldflags='-s -w -buildid=' \
    -o /out/clipp-relay ./cmd/clipp-relay

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=10001:10001 /out/clipp-relay /clipp-relay
USER 10001:10001
ENTRYPOINT ["/clipp-relay"]
