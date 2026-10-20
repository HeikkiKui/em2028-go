FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/em2028 . \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/em2028 /em2028
COPY --from=build --chown=65532:65532 /out/data /data
ENV DB_PATH=/data/em2028.db ADDR=:8080
VOLUME ["/data"]
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/em2028"]
