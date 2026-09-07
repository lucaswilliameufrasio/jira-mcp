# syntax=docker/dockerfile:1

FROM golang:1.27.1-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/jira-mcp ./cmd/jira-mcp

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/jira-mcp /usr/local/bin/jira-mcp
ENTRYPOINT ["jira-mcp", "remote"]
