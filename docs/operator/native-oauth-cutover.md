# Native OAuth cutover and signing-key recovery

Use this procedure only against an explicitly approved installation. The
authentication modernization requires one-time app reauthorization. It does not
reset Relay Accounts, administrator approval, Portal Sessions, Quota Plans,
Recorded Relay Usage, Relay Configurations or Pairing.

## Initial cutover

1. Verify the target database, schema lineage, backup and migration/serving role
   identities. Stop the relay and fence every older writer before applying a
   schema change. Do not run old and new binaries against the same database.
2. Provision a stable 32-byte random OAuth signing key in the dedicated Secret
   referenced by `credentials.nativeSigningKeySecret`. Keep the key bytes
   outside Git and operator receipts. Do not generate a key during application
   startup.
3. Apply the application migrations with the migration identity:

   ```sh
   go run ./cmd/clipp-relay -command migrate -config /path/to/migration-config.json
   ```

4. Run the explicit credential reset with the same migration identity and the
   mounted signing Secret:

   ```sh
   go run ./cmd/clipp-relay -command reset-auth -config /path/to/migration-config.json
   ```

   A successful exit records the key identity, completion time and
   `credential_reset` audit event in one transaction. The operation deletes
   old OAuth codes, refresh generations, Login Grants, Relay Access Tokens and
   native credentials. It does not erase Portal Sessions or increment account
   generations.
5. Grant the serving identity `SELECT` on `oauth_issuer_state` and the native
   table privileges in [database privileges](database-privileges.md). Set the
   chart's expected schema to 16 and start only the new binary. Readiness is
   withheld if the reset record is absent or the mounted key does not match it.
6. Restart once with the same Secret and verify serving readiness. Existing
   accounts can authorize their apps again using their existing Portal
   Sessions.

## Uncertain completion or key replacement

If `reset-auth` reports an error after the connection fails, leave serving
stopped and rerun it with the same Secret. The transaction's durable key
identity makes a committed retry a no-op; it cannot clear credentials issued
after that reset. Do not infer completion from process output alone.

To replace a signing key deliberately, stop and fence all writers, provision
the replacement, run `reset-auth` with that key, then update the serving Secret
and restart. Serving with an unexpected key fails closed. Restoring an earlier
key requires another explicit reset, which deletes current OAuth records before
serving; it cannot restore the old records.

Do not roll back to a binary that accepts legacy credentials or restore a
database snapshot containing spent credentials after the cutover. Recovery
must preserve the reset state and audit row and must follow the normal database
recovery process before serving resumes.
