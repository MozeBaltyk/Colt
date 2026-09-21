# Roadmap

Milestone 1 is normative and essentially complete. Its Git credential-helper and credential-free HTTPS scenarios, SSH transport semantics (authoritative SSH URLs, `git` SSH user, existing-user SSH environment, key-outs-of-storage, host aliases, and SSH-after-logout), status credential-source reporting, re-login advice on rejected stored credentials, credential redaction under a new bounded `--verbose` diagnostic, device-flow-over-SSH, and the no-echo secret-entry acceptance are all active. The only M1 scenario still tagged `@unimplemented` is the interactive plaintext-consent flow for an unavailable secure backend (`CORE-CREDENTIAL-005`). Milestone 2 core list, clone, transport, minimum release behavior, clone path-race confinement, and hostile local-config rejection are implemented. Milestone 3 template initialization, Milestone 4 workspace reconciliation, and Milestone 5 project health are implemented. Milestone 6 remains planned.

1. **M1, authentication and blank initialization:** configure and authenticate the currently supported providers (GitHub, GitLab, Gitea, Forgejo) with environment or manually entered credentials, automatic GitHub OAuth Device Flow for stored login when no reusable credential or token resolves, native secure persistence, explicit-consent plaintext fallback, local stored-credential logout, `auth.source`/`credential_id` references, concise live/offline status with stored/environment credential-source reporting, actionably re-login advice on rejected persisted credentials, a bounded non-secret `--verbose` (`-v`) diagnostic, deterministic credential resolution, and local and remote `colt init <project>` behavior from [shared core](00-core.md) and [project initialization](01-project-init.md). GitHub Device Flow uses Colt's embedded public OAuth app client ID and is independent of SSH or HTTPS Git transport. Gitea and Forgejo have real container-backed vertical tests. Provider-side revocation is implemented, reporting remote supported/unsupported/failed independently of local credential removal. Production native-backend persistence acceptance is gated behind the `integration` lane and characterized by a native OS keyring round-trip test that skips when no native credential facility is available.
2. **M2, project lifecycle:** provider-aware list, clone, and minimum release primitives from [project lifecycle](02-project-lifecycle.md), including fail-closed rejection of hostile repository-local Git configuration.
3. **M3, parameterized templates:** named, versioned, pinned, data-only template initialization from [template initialization](03-template-init.md) is implemented. M4 workspace reconciliation and M5 project health are also implemented; M6 remains planned.
4. **M7, run/deploy:** Gitea and Forgejo deployment/lifecycle orchestration is implemented (`colt run <gitea|forgejo> <name> [--replace]` plus `status`/`stop`/`start`/`rm`), including advertised external HTTPS URLs. Destructive live Gitea and Forgejo acceptance has passed, including persistence, lifecycle, and final cleanup. TLS termination, administrator/token creation, and provider onboarding remain external/manual as described in the [run/deploy spec](07-run-deploy.md). M7 is a prerequisite for M4 local-provider sync.
5. **M4, declarative workspace:** implemented provider/namespace reconciliation through top-level `colt status`, `colt sync [--dry-run]`, and one-shot `colt mirror`. Fake-backed acceptance is active; live local-provider mirroring remains an explicitly gated integration limitation.
6. **M5, project health:** implemented deterministic, diagnostic-only policy checks from [project health](05-project-health.md). Acceptance uses fake providers; live-provider coverage is not claimed.
7. **M6, analyzer:** retain bounded future intent in [analyzer](06-analyzer.md).

## Deferred And Future

- User-only ACL validation before enabling the plaintext credential fallback on Windows; macOS Keychain builds require CGO in the current M1 implementation.
- Additional providers.
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

Gitea and Forgejo are implemented as independent provider adapters and covered by real container-backed initialization/push scenarios. Each provider MUST remain independently identifiable behind the existing provider abstraction (`CORE-PROVIDER-007`, `CORE-PROVIDER-010`); Forgejo MUST NOT be folded into the Gitea adapter as a mere alias. Adding future providers SHOULD primarily mean implementing their own adapter plus provider-specific tests, rather than changing Colt's core project, credential, workspace, or Git transport models. Authorization mechanisms, endpoint mappings, scopes, token formats, pagination, release details, and error mappings stay unspecified until the adapter is designed.
