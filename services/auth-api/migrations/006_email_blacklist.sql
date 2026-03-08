-- M6-02: email bounce blacklist

create table if not exists email_blacklist (
  id text primary key,
  email text not null unique,
  reason text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_email_blacklist_email
  on email_blacklist(email);
