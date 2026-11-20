# syntax=docker/dockerfile:1
FROM ccr.ccs.tencentyun.com/solat/golang:1.26-alpine AS build
WORKDIR /src

# Mainland networks often time out on proxy.golang.org; allow override at build time.
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB}

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/tracking ./cmd/tracking

FROM ccr.ccs.tencentyun.com/solat/alpine:3.20

RUN apk add --no-cache ca-certificates wget \
 && adduser -D -H -u 10001 appuser

WORKDIR /app
COPY --from=build /out/tracking /app/tracking
# One binary; invoked as tracking-worker it defaults to TRACKING_ROLE=worker.
RUN ln -s tracking /app/tracking-worker && chown -R appuser:appuser /app

ENV HTTP_ADDR=:8092
EXPOSE 8092
USER appuser
CMD ["/app/tracking"]
