# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
WORKDIR /src
COPY go.work ./
COPY modules/common-go ./modules/common-go
COPY services/auth-api ./services/auth-api
WORKDIR /src/services/auth-api
RUN go mod download && go build -o /out/auth-api ./cmd/auth-api

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/auth-api /app/auth-api
EXPOSE 8080
ENTRYPOINT ["/app/auth-api"]
