# Stage 1: Build Go binary
FROM golang:1.23-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/wahi-api ./cmd/server

# Stage 2: Minimal distroless runtime
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /out/wahi-api /app/wahi-api
COPY --from=builder /src/testdata /app/testdata

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/app/wahi-api"]
CMD ["serve"]
