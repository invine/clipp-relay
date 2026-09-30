\set ON_ERROR_STOP on
REVOKE ALL ON SCHEMA public FROM PUBLIC;
SELECT format('GRANT USAGE ON SCHEMA public TO %I', :'serving_user') \gexec
SELECT format('GRANT USAGE, CREATE ON SCHEMA public TO %I', :'migration_user') \gexec
SELECT format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %I', :'migration_user', :'serving_user') \gexec
SELECT format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO %I', :'migration_user', :'serving_user') \gexec
-- An existing serving role must not retain DDL through grants, membership, or
-- ownership. Fail closed instead of silently rewriting an unexpected database.
SELECT NOT EXISTS (
  SELECT 1 FROM pg_auth_members WHERE member = (SELECT oid FROM pg_roles WHERE rolname = :'serving_user')
) AND NOT has_database_privilege(:'serving_user', current_database(), 'CREATE')
  AND NOT EXISTS (
    SELECT 1 FROM pg_namespace n
    WHERE n.nspname NOT LIKE 'pg_%' AND n.nspname <> 'information_schema'
      AND has_schema_privilege(:'serving_user', n.oid, 'CREATE')
  )
  AND NOT EXISTS (
    SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE c.relowner = (SELECT oid FROM pg_roles WHERE rolname = :'serving_user')
      AND n.nspname NOT LIKE 'pg_%' AND n.nspname <> 'information_schema'
  )
  AND NOT EXISTS (
    SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE p.proowner = (SELECT oid FROM pg_roles WHERE rolname = :'serving_user')
      AND n.nspname NOT LIKE 'pg_%' AND n.nspname <> 'information_schema'
  ) AS serving_no_ddl \gset
\if :serving_no_ddl
\else
  DO $$ BEGIN RAISE EXCEPTION 'existing serving role retains DDL authority'; END $$;
\endif
