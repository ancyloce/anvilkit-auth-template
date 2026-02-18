# Security Policy

## Reporting a Vulnerability

Please report vulnerabilities privately by opening a security advisory or contacting maintainers directly.
Include:

- Affected version/commit
- Reproduction steps
- Impact assessment
- Suggested mitigation (optional)

We will acknowledge within 3 business days and provide status updates until remediation.

## Security Notes

- JWT uses HS256. Use a strong `JWT_SECRET` in all non-dev environments.
- Refresh tokens are stored only as SHA-256 hashes (`refresh_tokens.token_hash`).
- Passwords are hashed with bcrypt cost 12.
