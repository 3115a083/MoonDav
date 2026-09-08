## Summary

Describe the change and why it is needed.

## Security impact

- [ ] No new public listener, credential path, filesystem write, or privilege is introduced.
- [ ] Authentication and authorization changes are covered by tests.
- [ ] No secret, token, password, or private URL is included in code, logs, fixtures, or screenshots.
- [ ] Container hardening remains intact.

## Validation

- [ ] `go test ./...`
- [ ] Multi-architecture Docker build
- [ ] WebDAV behavior checked when relevant
- [ ] README / security documentation updated when behavior changes
