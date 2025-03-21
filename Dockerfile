FROM golang:1.21 as builder
WORKDIR /app
COPY go.mod ./
COPY go.sum ./
COPY cmd/*.go ./
RUN go mod download && CGO_ENABLED=0 GOOS=linux go build -o /helm-version-check

FROM alpine:latest
WORKDIR /app
COPY --from=builder /helm-version-check /app
EXPOSE 9080
ENV NAMESPACE=argocd
ENTRYPOINT ["/app/helm-version-check", "--verbose"]
