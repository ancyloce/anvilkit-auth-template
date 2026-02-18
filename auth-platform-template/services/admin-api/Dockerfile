# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
WORKDIR /src
COPY go.work ./
COPY modules/common-go ./modules/common-go
COPY services/admin-api ./services/admin-api
WORKDIR /src/services/admin-api
RUN go mod download && go build -o /out/admin-api ./cmd/admin-api

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/admin-api /app/admin-api
COPY --from=build /src/services/admin-api/internal/rbac /app/internal/rbac
EXPOSE 8081
ENTRYPOINT ["/app/admin-api"]
