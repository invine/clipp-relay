\set ON_ERROR_STOP on
SELECT format('CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L', :'serving_user', :'serving_password') WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'serving_user') \gexec
SELECT format('CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L', :'migration_user', :'migration_password') WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'migration_user') \gexec
SELECT count(*) = 2 AND bool_and(rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls) AND NOT EXISTS (
  SELECT 1 FROM pg_roles privileged
  WHERE (privileged.rolsuper OR privileged.rolcreatedb OR privileged.rolcreaterole OR privileged.rolreplication OR privileged.rolbypassrls OR privileged.rolname = :'migration_user')
    AND pg_has_role((SELECT oid FROM pg_roles WHERE rolname = :'serving_user'), privileged.oid, 'MEMBER')
) AS safe_roles FROM pg_roles WHERE rolname IN (:'serving_user', :'migration_user') \gset
\if :safe_roles
\else
  \echo 'existing application role has unsafe privileges'
  \quit 1
\endif
SELECT format('CREATE DATABASE %I OWNER %I', :'dbname', :'migration_user') WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'dbname') \gexec
SELECT datdba = (SELECT oid FROM pg_roles WHERE rolname = :'migration_user') AS owner_ok FROM pg_database WHERE datname = :'dbname' \gset
\if :owner_ok
\else
  \echo 'database owner differs from migration role'
  \quit 1
\endif
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'dbname') \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO %I, %I', :'dbname', :'serving_user', :'migration_user') \gexec
SELECT format('ALTER ROLE %I SET temp_file_limit = %L', :'serving_user', '64MB') \gexec
