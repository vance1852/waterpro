FROM golang:1.22.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/waterpro ./cmd/server \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/waterpro /app/waterpro
COPY --chown=nonroot:nonroot --from=build /out/data /data
VOLUME ["/data"]
ENV WATERPRO_HTTP_ADDR=:8080
ENV WATERPRO_DATABASE_PATH=/data/waterpro.db
EXPOSE 8080
ENTRYPOINT ["/app/waterpro"]
