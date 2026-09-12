# Roadmap

Milestone 1 is normative but only partially implemented; its absent behavior is explicitly tagged `@unimplemented`. Milestones 2 through 6 are planned and their `@planned` scenarios do not claim current command or release availability.

1.  **M1, authentication and blank initialization:** configure and authenticate the currently supported providers (GitHub, GitLab) with environment credentials, `auth.source`/`credential_id` references, concise live/offline status, deterministic credential resolution, and local and remote `colt init <project>` behavior from [shared core](00-core.md) and [project initialization](01-project-init.md). Persisted credentials, plaintext fallback, and logout remain normative but `@unimplemented`; browser/device authentication and provider-side revocation are optional and `@unimplemented`. Environment credentials are the production path today.
2.  **M2, project lifecycle:** add provider-aware list, clone, and minimum release primitives from [project lifecycle](02-project-lifecycle.md).
3.  **M3, parameterized templates:** add named, versioned, data-only template initialization from [template initialization](03-template-init.md).
4.  **M4, declarative workspace:** reconcile provider/namespace repository selections through top-level `colt status` and `colt sync [--dry-run]` as specified in [workspace reconciliation](04-workspace.md).
5.  **M5, project health:** add deterministic diagnostic policy checks from [project health](05-project-health.md).
6.  **M6, analyzer:** retain bounded future intent in [analyzer](06-analyzer.md).

## Deferred And Future

- Additional providers: Gitea and Forgejo as new provider adapters (see below).
- Additional package ecosystems.
- Advanced credential management beyond M1 persistence, credential migration UX, multiple simultaneous interactive identities per alias, provider credential inventory, automatic remote token rotation, advanced enterprise SSO behavior, provider token administration, Git-driven credential enrollment (`store` persisting unknown credentials), explicit `erase`-to-logout lifecycle semantics, global helper integration mode, SSH key generation/management, and advanced enterprise authentication.
- General JSON output, shell completion, and extended transport preferences (the deterministic `CORE-GIT-009` preference model itself is specified; only extensions are deferred).
- Advanced release notes, changelog integration, and signed tags.
- Destructive workspace pruning and bulk fetch/update.
- Project-health auto-remediation.
- Executable template hooks and plugins.
- Analyzer collection, schema, report formats, and other implementation detail.
- GUI, plugin marketplace, arbitrary CI generation, and repository hosting.

Required provider-specific CLIs and replacement of native Git are out of scope.

## Future Provider Expansion

Gitea and Forgejo are planned future provider integrations. They are not part of
any active milestone and no behavior in this roadmap claims their availability.

Each MUST arrive as an independently identifiable provider adapter behind the
existing provider abstraction (`CORE-PROVIDER-007`, `CORE-PROVIDER-010`); Forgejo
MUST NOT be folded into the Gitea adapter as a mere alias. Adding either type
SHOULD primarily mean implementing a new adapter plus its provider-specific tests,
rather than changing Colt's core project, credential, workspace, or Git transport
models. Their authorization mechanisms, endpoint mappings, scopes, token formats,
pagination, release details, and error mappings stay unspecified until the adapters
are designed.
