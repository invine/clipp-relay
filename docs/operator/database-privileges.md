# Serving role table privileges

The migration identity owns schema objects and applies migrations. The serving
identity has no database or schema DDL authority. Keep its table privileges
explicit when adding tables; migration SQL revokes `PUBLIC` access.

For the native Fosite grant tables introduced at schema revision 15, the
serving identity needs these data privileges:

| Table | Privileges | Use |
| --- | --- | --- |
| `native_authorization_codes` | `SELECT`, `INSERT`, `UPDATE` | Resolve digest, issue code and consume it once. |
| `native_login_grants` | `SELECT`, `INSERT`, `UPDATE` | Create a grant, rotate its refresh generation, track idle expiry and terminate on replay. |
| `native_refresh_generations` | `SELECT`, `INSERT`, `UPDATE` | Resolve refresh digests, record successors and consume generations. |
| `native_access_tokens` | `SELECT`, `INSERT`, `DELETE` | Resolve active tokens, issue tokens and revoke a grant's tokens on replay. |

The disposable PostgreSQL setup in `scripts/smoke-postgres.sh` applies this
allowlist to each supported server version. Do not grant access to `PUBLIC` or
give the serving role ownership of these tables.
