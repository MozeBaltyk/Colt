# Template Initialization Specification

**Milestone 3 is planned and not implemented.** Its scenarios describe intended acceptance behavior, not current command or release availability. Template initialization extends [blank initialization](01-project-init.md) without changing its provider resolution, identity, safety, conflict, or partial-failure rules.

``` text
colt init <project> --template <name>[@<version>] [--set <key>=<value>]... [--local] [--provider <alias>]
colt template list
colt template show <name>[@<version>]
```

Templates are configured named sources. A version pin always resolves the same immutable source version; an unpinned name may resolve according to configuration. Colt does not prescribe a registry or transport protocol in this milestone.

| ID                    | Planned requirement                                                                                                                                                                                                                                                                                | Acceptance specification                                                            |
|:----------------------|:---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------------------------------|
| `TEMPLATE-SOURCE-001` | Colt **MUST** resolve configured named templates and immutable version pins deterministically. `colt template list` and `colt template show` **MUST** inspect configured metadata without materializing or executing template content.                                                             | [`template_initialization.feature`](../../features/template_initialization.feature) |
| `TEMPLATE-PARAM-001`  | Templates **MUST** declare string parameters with required or default behavior. Interactive use **MUST** prompt only for unresolved declared values; noninteractive `--set` **MUST** supply declared values. Unknown parameters or missing required values **MUST** be rejected before mutation.   | [`template_initialization.feature`](../../features/template_initialization.feature) |
| `TEMPLATE-SAFETY-001` | Interpolation **MUST** be deterministic and data-only and **MUST NOT** evaluate arbitrary expressions, plugins, hooks, or source content. Materialization **MUST NOT** import source `.git` metadata or credential material and **MUST** reject output paths or links that escape the destination. | [`template_initialization.feature`](../../features/template_initialization.feature) |
| `TEMPLATE-INIT-001`   | Template initialization **MUST** preserve the source, create fresh Git history without inherited remotes, commit only validated materialized content, and follow shared initialization safety and partial-failure behavior.                                                                        | [`template_initialization.feature`](../../features/template_initialization.feature) |

Executable template hooks and plugins remain deferred.
