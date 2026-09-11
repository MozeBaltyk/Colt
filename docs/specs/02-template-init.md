# Template Initialization Specification

This intermediate milestone follows successful blank initialization. It is
design-level and does not define production acceptance criteria.

```text
colt init <project> --template <name> [--local] [--provider <alias>]
```

The first supported template is `general`. Template initialization follows the
same provider resolution, identity, safety, conflict, and partial-failure rules
as [blank initialization](01-project-init.md).

Colt materializes tracked template content without the template's `.git`
directory, history, credentials, or remotes. The destination is a new repository
with new history and an initial commit containing the materialized content. A
missing or inaccessible template is detected before remote creation when
practical. Template source state is never modified.

Named custom templates, variables, and version pinning remain future options.
