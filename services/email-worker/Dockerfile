# syntax=docker/dockerfile:1
FROM golang:1.23 AS build
WORKDIR /src

# Copy workspace metadata and referenced modules.
COPY go.work /src/go.work
COPY modules/common-go /src/modules/common-go
COPY services/auth-api /src/services/auth-api
COPY services/admin-api /src/services/admin-api
COPY services/email-worker /src/services/email-worker
COPY tools/healthcheck /src/tools/healthcheck

# Remove root workspace entry for container builds to avoid depending on root go.mod/go.sum.
RUN go work edit -dropuse . && go work sync

WORKDIR /src/services/email-worker
RUN go mod download && go build -o /out/email-worker ./cmd/worker

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/email-worker /app/email-worker
ENTRYPOINT ["/app/email-worker"]
