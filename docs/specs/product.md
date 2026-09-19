# Colt Product Specification

Colt is a provider-independent project manager for Git repositories. Colt talks directly to supported Git hosting providers through their HTTP APIs for hosting operations and uses native Git for repository operations; it is not another `gh` or `glab` command surface. GitHub, GitLab, Gitea, and Forgejo are currently supported (see [roadmap](90-roadmap.md)).

Provider API authentication, Git transport authentication, and Git commit identity are three separate concepts. Colt owns provider authentication, credential resolution, and direct provider API integration. Native Git owns repository mechanics and Git transport. Environment credentials are implemented today. The normative M1 design also specifies persisted credentials through a subsystem separate from normal configuration, local logout of Colt-owned credentials, and automatic GitHub OAuth Device Flow for effective stored authentication when no reusable credential or token resolves; remaining absent acceptance scenarios stay explicitly `@unimplemented`. Provider-side revocation is implemented, reporting remote supported/unsupported/failed independently of local credential removal; production native persistence acceptance is characterized in the `integration` lane. Colt embeds its public GitHub OAuth app client ID, so users do not configure one. Device Flow supplies an API token regardless of Git transport; SSH keys do not authenticate provider API calls. Provider configuration references credential sources but does not contain reusable secrets.

``` text
                           Colt CLI
                              |
            +-----------------+-----------------+
            |                 |                 |
         Config            Git Ops         Provider Ops
            |                 |                 |
   provider/account/         git       +--------+--------+- - - - -+
   namespace/identity/                 |                 |         |
   defaults                       GitHub HTTP       GitLab HTTP  Gitea HTTP
                                                                Forgejo
```

Colt owns configuration, workflow, and provider integration. Provider HTTP APIs are authoritative for hosted resources; `git` is authoritative for repository mechanics. Provider-independent code uses `namespace`; provider-native terms remain at integration boundaries.

## Source Of Truth

When documents differ, use this order:

1.  Active normative requirements in [shared core](00-core.md), [project initialization](01-project-init.md), and [project lifecycle](02-project-lifecycle.md).
2.  Active-MVP [Gherkin acceptance specifications](../../features/), excluding scenarios explicitly tagged `@planned` or `@unimplemented`.
3.  Planned capability specifications and their `@planned` scenarios: [templates](03-template-init.md), [workspace reconciliation](04-workspace.md), [project health](05-project-health.md), and [analyzer](06-analyzer.md).
4.  [Roadmap](90-roadmap.md).

Code and released behavior do not become available merely because they are specified here. When an active normative document changes behavior covered by an existing acceptance specification, that acceptance specification must be updated before the change is considered internally consistent.

## Milestones

| Order | Capability                                       | Detail                    |
|:------|:-------------------------------------------------|:--------------------------|
| M1 | Environment provider authentication (GitHub, GitLab, Gitea, Forgejo) and blank initialization | Partially implemented; Gitea/Forgejo have real container-backed vertical tests; provider-side revocation is implemented; production native persistence is `integration`-gated; other absent behavior remains tagged `@unimplemented` |
| M2 | Project lifecycle | Implemented — core list, clone, transport, minimum release, clone path-race confinement, and hostile repository-local Git configuration rejection are active |
| M3 | Parameterized template initialization | Planned |
| M7 | Run/deploy — self-hosted Gitea/Forgejo via systemd + podman | Planned |
| M4 | Declarative workspace reconciliation | Planned |
| M5 | Project health | Planned |
| M6 | Analyzer | Bounded future intent |
