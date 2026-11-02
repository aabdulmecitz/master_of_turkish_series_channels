FROM golang:1.22-alpine AS builder

WORKDIR /src/master-go

COPY master-go/go.mod master-go/go.sum* ./
RUN go mod download

COPY master-go/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/telefleet-master .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

COPY --from=builder /bin/telefleet-master /app/telefleet-master
COPY web-player /app/web-player

EXPOSE 8080

CMD ["/app/telefleet-master"]

