FROM golang:1.14

WORKDIR /go/src/github.com/compose-spec/compose-ref

ARG GOLANGCILINT_VERSION=v1.23.8
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin ${GOLANGCILINT_VERSION}
RUN go get -u github.com/kunalkushwaha/ltag

ENV GOFLAGS=-mod=vendor
COPY . .
