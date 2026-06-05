# Standalone build-from-source image. Release images are built by
# goreleaser from Dockerfile.goreleaser with the exact release binary.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
ENV GOOS=$TARGETOS
ENV GOARCH=$TARGETARCH
ENV CGO_ENABLED=0

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY cmd cmd
COPY internal internal
RUN go build -trimpath \
      -ldflags="-s -w \
        -X github.com/wilfriedroset/a10r/cmd.version=${VERSION} \
        -X github.com/wilfriedroset/a10r/cmd.commit=${COMMIT} \
        -X github.com/wilfriedroset/a10r/cmd.date=${DATE}" \
      -o /out/a10r .

# distroless/static over scratch: CA certs, tzdata, and the nonroot
# user come from a maintained base that is rebuilt when they go stale.
FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/a10r /usr/local/bin/a10r

ENTRYPOINT ["/usr/local/bin/a10r"]
