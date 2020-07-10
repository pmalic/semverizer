#
#
#
FROM golang:1.14-alpine AS builder

WORKDIR /go/src/github.com/pmalic/semverizer

COPY . .

RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -a

#
#
#
FROM alpine:latest

WORKDIR /semverizer

COPY --from=builder /go/src/github.com/pmalic/semverizer/semverizer /semverizer/

ENTRYPOINT ["/semverizer/semverizer"]
