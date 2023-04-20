FROM golang:1.19 as builder

WORKDIR /usr/src/app

COPY ../../../go.mod ../../../go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN go build -v -o /usr/local/bin/go-at-logstream .

FROM alpine:edge

COPY --from=builder /usr/local/bin/go-at-logstream /usr/local/bin/go-at-logstream
CMD ["/usr/local/bin/go-at-logstream"]
