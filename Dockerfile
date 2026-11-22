# MARKYS PDV SaaS — container Linux (PostgreSQL)
FROM golang:1.23-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod ./
RUN go mod download
COPY . .
# Verbose build output makes compiler diagnostics visible in Render Build Logs.
RUN go version && go env GOOS GOARCH CGO_ENABLED && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v -x -trimpath -ldflags="-s -w" -o /out/markys-pdv-saas ./cmd/pdv-saas

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/markys-pdv-saas /app/markys-pdv-saas
ENV SAAS_ADDR=:8080
ENV SAAS_MODE=postgres
ENV SAAS_COOKIE_SECURE=true
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/markys-pdv-saas"]
