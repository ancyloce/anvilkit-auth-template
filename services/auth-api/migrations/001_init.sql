create table if not exists tenants (
  id text primary key,
  name text not null,
  created_at timestamptz not null default now()
);

create table if not exists users (
  id text primary key,
  email text not null unique,
  password_hash text not null,
  created_at timestamptz not null default now()
);

create table if not exists tenant_users (
  tenant_id text not null references tenants(id) on delete cascade,
  user_id text not null references users(id) on delete cascade,
  created_at timestamptz not null default now(),
  primary key (tenant_id, user_id)
);

create table if not exists user_roles (
  tenant_id text not null references tenants(id) on delete cascade,
  user_id text not null references users(id) on delete cascade,
  role text not null,
  created_at timestamptz not null default now(),
  primary key (tenant_id, user_id, role)
);

create table if not exists refresh_tokens (
  token_hash text primary key,
  user_id text not null references users(id) on delete cascade,
  tenant_id text not null references tenants(id) on delete cascade,
  expires_at timestamptz not null,
  revoked_at timestamptz,
  created_at timestamptz not null default now()
);

create index if not exists idx_refresh_tokens_user_tenant on refresh_tokens(user_id, tenant_id);
create index if not exists idx_refresh_tokens_expires on refresh_tokens(expires_at);
create index if not exists idx_user_roles_tenant_user on user_roles(tenant_id, user_id);
