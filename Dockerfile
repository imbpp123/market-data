FROM golang:1.27.1-alpine3.24@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS build
WORKDIR /src
ENV GOTOOLCHAIN=local CGO_ENABLED=0
COPY go.mod go.sum ./
COPY api/go/go.mod api/go/go.sum ./api/go/
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
COPY api/go/marketdata ./api/go/marketdata
RUN go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/market-data-service ./cmd/market-data-service

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/market-data-service /market-data-service
USER 65532:65532
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/market-data-service", "-config", "/etc/market-data/config.yaml", "-healthcheck"]
ENTRYPOINT ["/market-data-service"]
CMD ["-config", "/etc/market-data/config.yaml"]
