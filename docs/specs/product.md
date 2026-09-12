# Colt Product Specification

Colt is a provider-independent project manager for Git repositories. Colt talks directly to supported Git hosting providers through their HTTP APIs for hosting operations and uses native Git for repository operations; it is not another `gh` or `glab` command surface. GitHub and GitLab are the currently supported providers; Gitea and Forgejo are planned future providers (see [roadmap](90-roadmap.md)).

Provider API authentication, Git transport authentication, and Git commit identity are three separate concepts. Colt owns provider authentication, credential resolution, and direct provider API integration. Native Git owns repository mechanics and Git transport. Environment credentials are implemented today. The normative M1 design also specifies persisted credentials through a subsystem separate from normal configuration and local logout of Colt-owned credentials; those scenarios are explicitly `@unimplemented`, as are optional browser/device authentication and provider-side revocation. Provider configuration references credential sources but does not contain reusable secrets.

``` text
                          Colt CLI
                             |
           +-----------------+-----------------+
           |                 |                 |
        Config            Git Ops         Provider Ops
           |                 |                 |
  provider/account/         git       +--------+--------+- - - - -+
  namespace/identity/                 |                 |         |
  defaults                       GitHub HTTP       GitLab HTTP  Gitea/Forgejo
                                                               [planned]
```

Colt owns configuration, workflow, and provider integration. Provider HTTP APIs are authoritative for hosted resources; `git` is authoritative for repository mechanics. Provider-independent code uses `namespace`; provider-native terms remain at integration boundaries.

## Source Of Truth

When documents differ, use this order:

1.  Active normative requirements in [shared core](00-core.md) and [project initialization](01-project-init.md).
2.  Active-MVP [Gherkin acceptance specifications](../../features/), excluding scenarios explicitly tagged `@planned` or `@unimplemented`.
3.  Planned capability specifications and their `@planned` scenarios: [project lifecycle](02-project-lifecycle.md), [templates](03-template-init.md), [workspace reconciliation](04-workspace.md), [project health](05-project-health.md), and [analyzer](06-analyzer.md).
4.  [Roadmap](90-roadmap.md).

Code and released behavior do not become available merely because they are specified here. When an active normative document changes behavior covered by an existing acceptance specification, that acceptance specification must be updated before the change is considered internally consistent.

## Milestones

| Order | Capability                                       | Detail                    |
|:------|:-------------------------------------------------|:--------------------------|
| M1    | Environment provider authentication (GitHub, GitLab) and blank initialization | Partially implemented; absent behavior is tagged `@unimplemented` |
| M2    | Project lifecycle                                | Planned                   |
| M3    | Parameterized template initialization            | Planned                   |
| M4    | Declarative workspace reconciliation             | Planned                   |
| M5    | Project health                                   | Planned                   |
| M6    | Analyzer                                         | Bounded future intent     |
