<!-- Describe what this PR changes and why. -->

## Summary

## Checklist

- [ ] `make check` passes (vet + tests + boundary lint)
- [ ] `go test ./... -race` passes
- [ ] Adapters return only `sdk/model` types and do not import openits-models
      (ADR 0002 — only `internal/wire` may)
- [ ] New/changed adapter behavior ships recorded fixtures with golden tests —
      **no fixtures, no merge** (ADR 0008); recordings scrubbed of
      deployment-identifying data
- [ ] Config or subject-grammar changes are validated at boot, not at publish
      time
- [ ] Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
      (`feat:` / `fix:` / `feat!:`) — the changelog is generated from them,
      don't hand-edit `CHANGELOG.md`
