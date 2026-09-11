# Colt Product Specification

Colt is a provider-independent Git project CLI that talks directly to
hosting-provider APIs and uses native Git for repository operations.

```text
                         Colt CLI
                            |
          +-----------------+-----------------+
          |                 |                 |
       Config            Git Ops         Provider Ops
          |                 |                 |
 provider/account/         git       +--------+--------+
 namespace/identity/                 |                 |
 defaults                       GitHub HTTP       GitLab HTTP
```

Colt owns configuration, workflow, and provider integration. Provider HTTP APIs
are authoritative for hosted resources; `git` is authoritative for repository
mechanics. Provider-independent code uses `namespace`; provider-native terms
remain at integration boundaries.

## Source Of Truth

When documents differ, use this order:

1. Active normative requirements in [shared core](00-core.md) and [project
   initialization](01-project-init.md).
2. Active-MVP [Gherkin acceptance specifications](../../features/), which trace
   to those requirement IDs.
3. Design-level later specifications: [templates](02-template-init.md), [project
   lifecycle](03-project-lifecycle.md), and [analyzer](04-analyzer.md).
4. [Roadmap](90-roadmap.md).
5. [Design notes](notes.md), which are non-normative input.

Code and released behavior do not become available merely because they are
specified here.

## Milestones

| Order | Capability | Detail |
| --- | --- | --- |
| 1 | Blank cross-provider initialization | Detailed and normative |
| 2 | Template initialization | Design-level |
| 3 | Project lifecycle | Design-level |
| 4 | Analyzer | Future intent only |
