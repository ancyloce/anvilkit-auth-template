-- M1-02: AuthN core schema (minimal account model + refresh rotation persistence)

-- users table is introduced in 001_init.sql in this template.
-- Keep migration idempotent and align legacy shape with AuthN requirements.
alter table if exists users
  alter column email drop not null;

alter table if exists users
  add column if not exists phone text,
  add column if not exists status smallint not null default 1,
  add column if not exists updated_at timestamptz not null default now();

create unique index if not exists idx_users_phone_unique
  on users(phone)
  where phone is not null;

-- Move password hash into dedicated credentials table.
create table if not exists user_password_credentials (
  user_id text primary key references users(id) on delete cascade,
  password_hash text not null,
  updated_at timestamptz not null default now()
);

insert into user_password_credentials (user_id, password_hash)
select u.id, u.password_hash
from users u
where u.password_hash is not null
  and not exists (
    select 1
    from user_password_credentials upc
    where upc.user_id = u.id
  );

-- Persist refresh token rotation chain.
create table if not exists refresh_sessions (
  id text primary key,
  user_id text not null references users(id) on delete cascade,
  token_hash text not null unique,
  user_agent text,
  ip text,
  expires_at timestamptz not null,
  revoked_at timestamptz,
  replaced_by text references refresh_sessions(id),
  created_at timestamptz not null default now()
);

create index if not exists idx_refresh_sessions_user_id on refresh_sessions(user_id);
create index if not exists idx_refresh_sessions_expires_at on refresh_sessions(expires_at);
create index if not exists idx_refresh_sessions_replaced_by on refresh_sessions(replaced_by);
