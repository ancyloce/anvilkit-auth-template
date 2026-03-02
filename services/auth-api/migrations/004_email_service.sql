-- M4-01: email service schema

create table if not exists email_verifications (
  id text primary key,
  user_id text not null references users(id) on delete cascade,
  token_hash text not null unique,
  token_type text not null,
  expires_at timestamptz not null,
  verified_at timestamptz,
  attempts integer not null default 0,
  created_at timestamptz not null default now(),
  constraint chk_email_verifications_token_type
    check (token_type in ('otp', 'magic_link'))
);

create index if not exists idx_email_verifications_user_id
  on email_verifications(user_id);
create index if not exists idx_email_verifications_expires_at
  on email_verifications(expires_at);

create table if not exists email_jobs (
  id text primary key,
  job_type text not null,
  status text not null,
  payload jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_email_jobs_status
  on email_jobs(status);
create index if not exists idx_email_jobs_created_at
  on email_jobs(created_at);

create table if not exists email_records (
  id text primary key,
  job_id text references email_jobs(id) on delete set null,
  user_id text references users(id) on delete set null,
  to_email text not null,
  template text,
  subject text,
  external_id text,
  status text not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists idx_email_records_job_id
  on email_records(job_id);
create index if not exists idx_email_records_user_id
  on email_records(user_id);
create index if not exists idx_email_records_external_id
  on email_records(external_id)
  where external_id is not null;
create index if not exists idx_email_records_created_at
  on email_records(created_at);

create table if not exists email_status_history (
  id text primary key,
  email_record_id text not null references email_records(id) on delete cascade,
  status text not null,
  message text,
  meta jsonb,
  created_at timestamptz not null default now(),
  constraint chk_email_status_history_status
    check (status in ('queued', 'sent', 'delivered', 'opened', 'clicked', 'bounced', 'failed'))
);

create index if not exists idx_email_status_history_record_created
  on email_status_history(email_record_id, created_at);
create index if not exists idx_email_status_history_created_at
  on email_status_history(created_at);

alter table if exists users
  add column if not exists email_verified_at timestamptz;

alter table if exists users
  alter column status set default 0;
