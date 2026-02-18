# Contributing

Thanks for contributing!

## Local development

```bash
cp .env.example .env
make init
make smoke
```

## PR checklist

- [ ] `go test ./... -count=1` passes
- [ ] `golangci-lint run` passes
- [ ] API responses follow envelope contract
- [ ] Docs/examples updated for API changes
