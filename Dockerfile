FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/simple-sub2api ./cmd/simple-sub2api

FROM alpine:3.20

RUN adduser -D -H -u 10001 simple-sub2api
WORKDIR /app
COPY --from=build /out/simple-sub2api /usr/local/bin/simple-sub2api
COPY configs/config.example.json /app/config.example.json
RUN mkdir -p /config && chown -R simple-sub2api:simple-sub2api /config
USER simple-sub2api
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/simple-sub2api"]
CMD ["--config", "/config/simple_sub2api.config.json", "--bind", "0.0.0.0:8080", "--allow-lan"]
