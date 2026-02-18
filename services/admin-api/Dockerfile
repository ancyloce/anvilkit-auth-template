# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
WORKDIR /src

# Copy workspace metadata so go.work can resolve every module it references.
COPY go.mod /src/go.mod
COPY go.sum /src/go.sum
COPY go.work /src/go.work

# Copy all modules referenced by go.work.
COPY modules/common-go /src/modules/common-go
COPY services/auth-api /src/services/auth-api
COPY services/admin-api /src/services/admin-api

RUN go work sync
WORKDIR /src/services/admin-api
RUN go mod download && go build -o /out/admin-api ./cmd/admin-api

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/admin-api /app/admin-api
COPY --from=build /src/services/admin-api/internal/rbac /app/internal/rbac
EXPOSE 8081
ENTRYPOINT ["/app/admin-api"]
