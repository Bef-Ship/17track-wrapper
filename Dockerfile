ARG GOLANG_IMAGE=ccr.ccs.tencentyun.com/solat/golang:1.25-alpine
ARG ALPINE_IMAGE=ccr.ccs.tencentyun.com/solat/alpine:3.20

FROM --platform=$BUILDPLATFORM ${GOLANG_IMAGE} AS build
ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/tracking-api ./cmd/api

FROM ${ALPINE_IMAGE}

RUN apk add --no-cache ca-certificates tzdata wget
WORKDIR /app
COPY --from=build /out/tracking-api /app/tracking-api

ENV HTTP_ADDR=:8092
EXPOSE 8092
USER nobody
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8092/health >/dev/null || exit 1
CMD ["/app/tracking-api"]
