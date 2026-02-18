# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
WORKDIR /src

# Copy workspace metadata and referenced modules.
COPY go.work /src/go.work
COPY modules/common-go /src/modules/common-go
COPY services/auth-api /src/services/auth-api
COPY services/admin-api /src/services/admin-api

# Remove root workspace entry for container builds to avoid depending on root go.mod/go.sum.
RUN go work edit -dropuse . && go work sync

WORKDIR /src/services/admin-api
RUN go mod download && go build -o /out/admin-api ./cmd/admin-api

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/admin-api /app/admin-api
COPY --from=build /src/services/admin-api/internal/rbac /app/internal/rbac
EXPOSE 8081
ENTRYPOINT ["/app/admin-api"]
