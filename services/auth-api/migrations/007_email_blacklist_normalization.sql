-- Enforce lowercase normalization for email_blacklist lookups/uniqueness.

update email_blacklist
set email = lower(email)
where email <> lower(email);

alter table if exists email_blacklist
  drop constraint if exists chk_email_blacklist_email_lower;

alter table if exists email_blacklist
  add constraint chk_email_blacklist_email_lower
  check (email = lower(email));

create unique index if not exists uq_email_blacklist_email_lower
  on email_blacklist(lower(email));
