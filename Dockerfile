FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /octroi ./cmd/octroi

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /octroi /usr/local/bin/octroi
COPY migrations/ /migrations/
EXPOSE 8080
ENTRYPOINT ["sh", "-c"]
CMD ["for i in $(seq 1 30); do octroi migrate --config /dev/null && break; echo 'Waiting for database...'; sleep 2; done && octroi ensure-admin --config /dev/null && octroi serve --config /dev/null"]
