# Template Initialization Specification

**Milestone 3 is implemented.** Template initialization extends [blank initialization](01-project-init.md) without changing its provider resolution, identity, safety, conflict, or partial-failure rules.

``` text
colt init <project> --template <name>[@<version>] [--set <key>=<value>]... [--local] [--provider <alias>]
colt template list
colt template show <name>[@<version>]
```

Templates are configured named sources. A version pin always resolves the same immutable source version; an unpinned name may resolve according to configuration. Colt does not prescribe a registry or transport protocol in this milestone.

| ID                    | Requirement                                                                                                                                                                                                                                                                                        | Acceptance specification                                                            |
|:----------------------|:---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------------------------------|
| `TEMPLATE-SOURCE-001` | Colt **MUST** resolve configured named templates and immutable version pins deterministically. `colt template list` and `colt template show` **MUST** inspect configured metadata without materializing or executing template content.                                                             | [`template_initialization.feature`](../../features/template_initialization.feature) |
| `TEMPLATE-PARAM-001`  | Templates **MUST** declare string parameters with required or default behavior. `--set` may supply values only for declared parameters; unknown parameters are rejected before mutation. In noninteractive operation, only required parameters without defaults must be explicitly supplied; parameters with declared defaults do not need `--set`. Interactive operation **MUST** prompt only for unresolved required values. Validation occurs before mutation; interpolation is deterministic and data-only.   | [`template_initialization.feature`](../../features/template_initialization.feature) |
| `TEMPLATE-SAFETY-001` | Interpolation **MUST** be deterministic and data-only and **MUST NOT** evaluate arbitrary expressions, plugins, hooks, or source content. Materialization **MUST NOT** import source `.git` metadata or credential material and **MUST** reject output paths or links that escape the destination. | [`template_initialization.feature`](../../features/template_initialization.feature) |
| `TEMPLATE-INIT-001`   | Template initialization **MUST** preserve the source, create fresh Git history without inherited remotes, commit only validated materialized content, and follow shared initialization safety and partial-failure behavior.                                                                        | [`template_initialization.feature`](../../features/template_initialization.feature) |

Executable template hooks and plugins remain deferred.

## Configuration

Templates are local directory sources. Relative `source` paths resolve from the directory containing the active config file; absolute paths are used as written. Names, versions, and parameter names are restricted identifiers. Every template has an explicit configured default version, and every version has a pinned SHA-256 digest.

```yaml
templates:
  service:
    default: "2"
    versions:
      "2":
        source: ./templates/service-2
        digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
        parameters:
          owner:
            required: true
          license:
            default: MIT
```

The digest is SHA-256 over entries sorted by slash-separated relative path, excluding every `.git` entry. Each entry contributes its type (`d` or `f`), NUL, path, NUL, four-digit permission mode, and NUL. A file additionally contributes its decimal byte length, NUL, and exact bytes. Symlinks and special files are invalid rather than hashed. This pins names, contents, entry types, and safe permission bits.

The only interpolation form is `{{parameter}}`, in regular-file contents and relative paths. The complete token must name a declared parameter. No expressions, environment expansion, evaluation, hooks, plugins, or source execution occur. Optional parameters without a default resolve to the empty string. Rendered paths must remain clean relative paths, cannot contain `.git`, and cannot collide. Colt validates the complete source and digest into memory before destination or provider mutation. Sources are bounded to 1,024 entries, 1 MiB per source file, 2 MiB per rendered file, 16 MiB total, and 4,096-byte paths.
