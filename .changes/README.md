# .changes

this directory marks the repository as managed by `ent release` (entwico devtools) —
the cross-tech, git-tag-based take on changesets. there is no config file by design.

## adding a changeset

run `ent release add` (or `ent release add --minor -m "..."`). each changeset is one file:

```
---
bump: minor
---

short, consumer-facing description of the change
```

commit the changeset together with the change it describes.

## releasing

- `ent release plan`   — dry run: shows the next version and the changelog
- `ent release apply`  — bumps, writes CHANGELOG.md, commits, tags `vX.Y.Z`, pushes
- `ent release revert` — undoes the last release (deletes the tag, resets the commit)

the version lives only in git tags (`v*`); one version for the whole repository.
