FROM golang:1.14.3-alpine AS builder
WORKDIR /go/src/s3sync2local
RUN apk add git curl
COPY src/*.go ./
COPY src/go.mod ./
RUN go mod vendor
RUN go build

FROM alpine:latest
LABEL maintainer="ttys3 <*@ttys3.net>"
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /go/src/s3sync2local/s3sync2local .
CMD ["./s3sync2local"]
