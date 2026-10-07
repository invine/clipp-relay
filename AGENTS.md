# Clipp Relay agent entry point

Clipp Relay owns account-scoped authorization and relay capacity; Device Network membership belongs to Clipp clients. Use the [glossary](GLOSSARY.md) for canonical terms and [service README](README.md) for implementation and protocol boundaries.

Start verification, qualification, operator setup, recovery or coordinated work from [project operations](docs/agents/operations.md). The cross-repository workflow has one [canonical tracker in Clipp](.scratch/agent-workflow/README.md); select that checkout explicitly rather than assuming a sibling path.

Preserve the accepted one-session-per-Peer-ID admission contract. Local checks and commits do not authorize publication, deployment, cluster mutation or live qualification. Preserve existing worktrees, stashes, profiles and private configuration; dated evidence establishes only its recorded revision and environment.
