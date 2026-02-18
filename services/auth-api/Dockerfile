# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
WORKDIR /src

COPY go.mod ./go.mod
COPY go.work ./go.work
COPY modules/common-go ./modules/common-go
COPY services/auth-api ./services/auth-api
COPY services/admin-api ./services/admin-api

RUN go work sync
WORKDIR /src/services/auth-api
RUN go mod download && go build -o /out/auth-api ./cmd/auth-api

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/auth-api /app/auth-api
EXPOSE 8080
ENTRYPOINT ["/app/auth-api"]
