# syntax=docker/dockerfile:1.7
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}     go build -trimpath -ldflags="-s -w" -o /out/moondav ./cmd/moondav

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/moondav /moondav
USER 65532:65532
EXPOSE 8765
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/moondav", "healthcheck"]
ENTRYPOINT ["/moondav"]
