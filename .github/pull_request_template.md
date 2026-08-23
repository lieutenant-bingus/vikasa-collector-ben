<!-- Describe what this PR changes and why. -->

## Summary

## Checklist

- [ ] `make check` passes (vet + tests + boundary lint)
- [ ] `go test ./... -race` passes
- [ ] Adapters return only `sdk/model` types and don't reach for
      openits-models — see [the boundary
      rule](../docs/reference/invariants.md#adapters-and-sdk-never-import-openits-models)
- [ ] New/changed adapter behavior ships recorded fixtures with golden
      tests, scrubbed of deployment-identifying data — see [the fixture
      rule](../docs/reference/invariants.md#no-fixtures-no-merge)
- [ ] Config or subject-grammar changes fail at boot, not at publish time —
      see [the trust-boundary
      rule](../docs/reference/invariants.md#config-is-the-trust-boundary-boot-fails-on-the-unrecognized)
- [ ] For an adapter PR: the diff touches only
      `internal/vendors/<vendor>/<kind>.go` + its test, one line in
      `RegisterAdapters`, and optionally `collector.yaml` — see the
      [review checklist](../.claude/skills/review-adapter-contribution/SKILL.md)
      for what a reviewer weighs beyond the machine checks
- [ ] Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
      (`feat:` / `fix:` / `feat!:`) — the changelog is generated from them,
      don't hand-edit `CHANGELOG.md`
