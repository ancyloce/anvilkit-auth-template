-- M2-01: multi-tenant schema hardening (tenants + tenant_users)

create table if not exists tenants (
  id text primary key,
  name text not null,
  slug text unique,
  status smallint not null default 1,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

alter table if exists tenants
  add column if not exists slug text,
  add column if not exists status smallint not null default 1,
  add column if not exists updated_at timestamptz not null default now();

create unique index if not exists idx_tenants_slug_unique
  on tenants(slug)
  where slug is not null;

create table if not exists tenant_users (
  tenant_id text not null references tenants(id) on delete cascade,
  user_id text not null references users(id) on delete cascade,
  role text not null default 'member',
  created_at timestamptz not null default now(),
  primary key (tenant_id, user_id),
  constraint chk_tenant_users_role check (role in ('owner', 'admin', 'member'))
);

alter table if exists tenant_users
  add column if not exists role text not null default 'member';

update tenant_users
set role = 'member'
where role is null;

alter table tenant_users
  alter column role set not null,
  alter column role set default 'member';

alter table if exists tenant_users
  drop constraint if exists chk_tenant_users_role;

alter table if exists tenant_users
  add constraint chk_tenant_users_role check (role in ('owner', 'admin', 'member'));

create index if not exists idx_tenant_users_user_id on tenant_users(user_id);
