# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/jira-mcp ./cmd/jira-mcp

FROM alpine:3.20
RUN adduser -D -H -u 10001 jira-mcp
COPY --from=build /out/jira-mcp /usr/local/bin/jira-mcp
USER jira-mcp
EXPOSE 8080
ENTRYPOINT ["jira-mcp", "remote"]
