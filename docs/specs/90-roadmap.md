# Roadmap

Specification detail follows implementation priority: the active milestone is
detailed and normative, the next milestone is design-level, later work states
capability boundaries, and future ideas remain non-committal. Later milestones
do not gain production acceptance criteria before implementation planning.

1. **Blank cross-provider initialization:** configure and authenticate supported
   providers, then deliver local and remote `colt init <project>` behavior from
   [shared core](00-core.md) and [project initialization](01-project-init.md).
2. **Templates:** add `colt init <project> --template <name>`, initially
   `general`, as described in [template initialization](02-template-init.md).
3. **Project lifecycle:** add provider-aware list, clone, sync, and minimum
   release behavior from [project lifecycle](03-project-lifecycle.md).
4. **Analyzer:** retain only the boundaries in [analyzer intent](04-analyzer.md)
   until project-management capabilities are established.

## Deferred And Future

- Custom templates, template variables, and template version pinning.
- Additional hosting providers and package ecosystems.
- OS credential-store integration; environment-based credentials cover MVP.
- JSON output, shell completion, dry-run behavior, and transport preferences.
- Advanced release generation, changelog integration, and signed tags.
- Analyzer collection, schema, report formats, and other implementation detail.
- GUI, plugin marketplace, arbitrary CI/CD generation, and repository hosting.

Required provider-specific CLIs and replacement of native Git are out of scope.
