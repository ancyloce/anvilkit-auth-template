# Email Deliverability (SPF, DKIM, DMARC)

This project sends transactional email (for example account verification) from the email worker. In production, mailbox providers expect your domain to publish valid SPF, DKIM, and DMARC DNS records. Without them, verification emails are more likely to be rejected or placed in spam.

Use this guide when configuring DNS for the sender domain used by `SMTP_FROM_EMAIL`.

## Recommended setup order

1. Choose a sender domain (example: `auth.example.com`) and sender address (example: `noreply@auth.example.com`).
2. Configure SPF for all legitimate sending systems.
3. Configure DKIM signing with a selector and publish the public key in DNS.
4. Configure DMARC reporting and policy rollout.
5. Validate records and send test messages to Gmail/Outlook/internal domains before production rollout.

## Environment guidance

- **Staging / test:** keep DMARC in monitor mode (`p=none`) while validating alignment and bounce behavior.
- **Production:** enforce DMARC with `p=reject` after verification traffic is stable.

---

## SPF

SPF tells receiving mail servers which hosts are authorized to send mail for your domain.

### Example SPF record

If `auth.example.com` sends via one SMTP relay IP plus an ESP include:

```dns
auth.example.com. 300 IN TXT "v=spf1 ip4:203.0.113.24 include:spf.mailprovider.example -all"
```

What this means:

- `ip4:203.0.113.24`: your self-managed outbound relay
- `include:spf.mailprovider.example`: your ESP's authorized sender set
- `-all`: fail mail from all other sources

### SPF implementation rules

- Publish **exactly one** SPF TXT record per domain.
- Keep policy explicit; avoid `+all` and broad patterns.
- Stay within the SPF DNS lookup limit (maximum 10 DNS-mechanism lookups).
- If multiple systems send mail, combine them into one record instead of adding multiple SPF TXT entries.

### Common SPF pitfalls

- Multiple SPF records on the same domain (causes SPF PermError).
- Using `~all` forever in production instead of moving to stricter policy.
- Chaining many `include:` values until lookup count exceeds limits.

---

## DKIM

DKIM adds a cryptographic signature to each email so receivers can verify the message was authorized by your domain and not modified in transit.

### Selectors

A selector allows multiple DKIM keys for one domain. Example selector: `s2026q1`.

DKIM DNS name pattern:

- `<selector>._domainkey.<domain>`
- Example: `s2026q1._domainkey.auth.example.com`

### Generate a DKIM key pair (2048-bit RSA)

```bash
openssl genrsa -out dkim-s2026q1-private.pem 2048
openssl rsa -in dkim-s2026q1-private.pem -pubout -out dkim-s2026q1-public.pem
```

Convert the public key into a single-line value (remove header/footer/newlines) and publish it in DNS.

### Example DKIM TXT record

```dns
s2026q1._domainkey.auth.example.com. 300 IN TXT "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAw8y8mY7...IDAQAB"
```

### DKIM operational practices

- Keep private keys only on mail-signing infrastructure/ESP; never in source control.
- Use at least 2048-bit keys in production.
- Rotate selectors periodically (for example quarterly or semi-annually):
  1. Add new selector + key.
  2. Start signing with the new selector.
  3. Remove old selector after mail signed by old key has aged out.

---

## DMARC

DMARC tells receivers what to do when SPF/DKIM checks fail and gives you aggregate/forensic reporting.

DMARC is published at `_dmarc.<domain>`.

### Alignment basics

DMARC passes only when **either SPF or DKIM passes with alignment**:

- SPF alignment: the SPF-authenticated domain matches (or is aligned with) the visible `From` domain.
- DKIM alignment: the DKIM signing domain (`d=`) matches (or is aligned with) the visible `From` domain.

### Staging example (`p=none`)

```dns
_dmarc.auth.example.com. 300 IN TXT "v=DMARC1; p=none; adkim=s; aspf=s; rua=mailto:dmarc-agg@example.com; ruf=mailto:dmarc-forensic@example.com; fo=1; pct=100"
```

Use this during rollout to collect reports and fix alignment before enforcement.

### Production example (`p=reject`)

```dns
_dmarc.auth.example.com. 300 IN TXT "v=DMARC1; p=reject; adkim=s; aspf=s; rua=mailto:dmarc-agg@example.com; ruf=mailto:dmarc-forensic@example.com; fo=1; pct=100"
```

`p=reject` should be the target production posture for transactional auth email to prevent spoofing and improve trust.

### DMARC rollout guidance

1. Start with `p=none` and valid `rua` mailbox.
2. Confirm most legitimate traffic is aligned and passing.
3. Move to `p=quarantine` briefly if needed.
4. Move to `p=reject` for production enforcement.
5. Keep monitoring aggregate reports for new send sources.

---

## Dedicated IP strategy

A dedicated IP is useful when your transactional volume is high enough that you need independent reputation control.

### Use a dedicated IP when

- You send large daily volume (steady, predictable traffic).
- OTP/verification delivery is business-critical.
- You need reputation isolation from other senders.

### Shared IP is usually enough when

- Volume is low or bursty.
- You are early-stage and do not have enough traffic to build dedicated reputation quickly.
- Your ESP's shared pool already has strong reputation and monitoring.

### Warm-up and tradeoffs

- New dedicated IPs require warm-up: start with low daily volume and increase gradually.
- Sudden volume spikes from a cold IP can cause throttling or spam placement.
- Dedicated IP gives control and isolation, but also operational burden (warm-up, reputation monitoring, incident handling).

---

## Troubleshooting bounce and spam issues

Use this checklist when verification emails are delayed, bounced, or landing in junk folders.

### 1) Authentication checks

- Verify SPF, DKIM, and DMARC records with `dig`/DNS tools.
- Confirm DKIM signatures are present and valid in raw message headers.
- Ensure DMARC alignment for the visible `From` domain.

Example checks:

```bash
dig +short TXT auth.example.com
dig +short TXT s2026q1._domainkey.auth.example.com
dig +short TXT _dmarc.auth.example.com
```

### 2) Bounce diagnostics

- **Hard bounce** (invalid user/domain): suppress immediately.
- **Soft bounce** (mailbox full, temporary block): retry with backoff, then suppress after repeated failures.
- Review SMTP response codes and enhanced status codes from your provider.

Common causes:

- Recipient domain does not exist / mailbox invalid.
- Sender domain missing or broken SPF/DKIM/DMARC.
- Sending IP/domain on a blocklist.

### 3) Spam/junk placement diagnostics

- Check domain/IP reputation in your ESP dashboard and major postmaster tools.
- Confirm reverse DNS/PTR and HELO/EHLO identity are sane if you run your own relay.
- Reduce spam-like content patterns (URL shorteners, misleading subject lines, image-only bodies).
- Ensure text and HTML bodies are both present and consistent.
- Keep send patterns stable; avoid sudden spikes.

### 4) DNS and configuration mistakes

- TTL too high during rollout, making fixes slow to propagate.
- Copy/paste errors in TXT records (wrapped quotes, split strings, whitespace mistakes).
- DKIM key published under wrong selector.
- DMARC record published on wrong host (must be `_dmarc.<domain>`).

### 5) Ongoing operational baseline

- Track bounce rate, complaint rate, and inbox placement over time.
- Alert on sudden increases in `bounced` or `failed` email statuses.
- Re-audit SPF includes and DKIM selectors whenever changing providers.

