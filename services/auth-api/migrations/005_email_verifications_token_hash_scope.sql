-- M5-02: scope email verification token hash uniqueness by token_type
-- OTP space is only 1,000,000 values. Global uniqueness on token_hash causes
-- false collisions across users over time.

alter table if exists email_verifications
  drop constraint if exists email_verifications_token_hash_key;

create unique index if not exists idx_email_verifications_magic_token_hash_unique
  on email_verifications(token_hash)
  where token_type = 'magic_link';

create index if not exists idx_email_verifications_otp_lookup
  on email_verifications(user_id, token_hash)
  where token_type = 'otp';
