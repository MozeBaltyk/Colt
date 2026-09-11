# Roadmap

Milestone 1 is implemented and normative. Milestones 2 through 6 are planned and their `@planned` scenarios do not claim current command or release availability.

1.  **M1, authentication and blank initialization:** configure and authenticate supported providers through persistent interactive authentication (secure credential storage with an explicit plaintext fallback), environment credentials, `auth.source`/`credential_id` credential references, local logout with optional `--revoke` provider-side revocation, concise live/offline status, and deterministic credential resolution, and deliver local and remote `colt init <project>` behavior from [shared core](00-core.md) and [project initialization](01-project-init.md). The persistence, fallback, and logout behavior is normative desired behavior and is NOT yet implemented; environment-only credentials remain the implemented path until the credential subsystem lands.
2.  **M2, project lifecycle:** add provider-aware list, clone, and minimum release primitives from [project lifecycle](02-project-lifecycle.md).
3.  **M3, parameterized templates:** add named, versioned, data-only template initialization from [template initialization](03-template-init.md).
4.  **M4, declarative workspace:** reconcile provider/namespace repository selections through top-level `colt status` and `colt sync [--dry-run]` as specified in [workspace reconciliation](04-workspace.md).
5.  **M5, project health:** add deterministic diagnostic policy checks from [project health](05-project-health.md).
6.  **M6, analyzer:** retain bounded future intent in [analyzer](06-analyzer.md).

## Deferred And Future

- Additional hosting providers and package ecosystems.
- Advanced credential management beyond M1 persistence, credential migration UX, multiple simultaneous interactive identities per alias, provider credential inventory, automatic remote token rotation, advanced enterprise SSO behavior, provider token administration, SSH key generation/management, and advanced enterprise authentication.
- General JSON output, shell completion, and transport preferences.
- Advanced release notes, changelog integration, and signed tags.
- Destructive workspace pruning and bulk fetch/update.
- Project-health auto-remediation.
- Executable template hooks and plugins.
- Analyzer collection, schema, report formats, and other implementation detail.
- GUI, plugin marketplace, arbitrary CI generation, and repository hosting.

Required provider-specific CLIs and replacement of native Git are out of scope.
