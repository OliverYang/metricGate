# Commit-replay summary data (dev-cockpit, 289 commits)

This directory holds the **summary form** of the commit-replay raw data that
`docs/claims.md` #19 and `docs/evaluations/evaluation-commit-replay-results.md`
depend on. It exists because of one measured fact: the raw data was **not in
git and had never been tracked** (`.data/` is gitignored and `git log --all
-- .data` is empty), so the numbers behind a published claim lived only on one
machine's disk.

The raw data is 1.3 GB of per-commit JSON (`.data/commit-replay/devcockpit/`).
It is not reproduced here. What is here:

| file | what it is |
|---|---|
| `summary.json` | per-rule totals, the criterion versions behind each number, the 13 rows of the old report's table that cannot be reproduced from the archive, and the full `test-no-assert` recalibration |
| `commits.txt` | the 289 full shas that were replayed (chronological: 5065169f69 initial commit 2026-05-14 … 396cd3cd8f 2026-10-08) |
| `commits-with-subjects.txt` | the same shas with date and subject, so an index (e.g. "index 75 = `444b31ec97`") can be checked without cloning anything |

## Three things to read before quoting any number from the original report

1. **A finding count is meaningless without the criterion version.**
   `summary.json` → `criterion_versions`. The archive's original run cannot be
   attributed to a metricgate commit; the closest reconstruction is
   `git show d5cb01c:policy.yaml` (157 lines) + a binary built at `d5cb01c`,
   which reproduces `396cd3cd8f`'s archived values exactly (2174 findings /
   555 files / 222 errors). Substituting the 186-line `policy.yaml` from
   `59e0a24^` inflated every metric count and added five rule families the
   archive has at zero — that mistake produced a false "the archive is not
   reproducible" conclusion in iter-30 before it was caught.

2. **13 of the 18 rows in the report's rule-family table are not reproducible
   from the archive** under any aggregation tried (findings, error-only,
   commits, distinct files, distinct anchors, metric snapshots above warn or
   error). `summary.json` → `unreproducible_rows` lists each pair. Read them as
   an unknown aggregation, not as a different measurement. The four rules the
   archive reports at zero are explained by the policy (one disabled, one
   java-only, one with `error: off`, one absent).

3. **"215/289 commits" is not 215 events.** All 215 are one function
   (`internal/eventbus/eventbus_test.go:40`,
   `TestPublishDoesNotBlockOnFullSubscriber`) counted once per commit in 215
   consecutive commits, introduced at index 75 (`444b31ec97`, 2026-07-15) and
   present in every commit after it. `summary.json` →
   `test_no_assert_recalibration.the_215_mechanism`.

## If the raw JSON reappears

`summary.json` → `reproduce` carries the commands: per-rule totals, the
era-correct single-commit replay, and the two-binary 289-commit loop (one clone
per binary, sequential checkouts — parallel shards on one clone fail on
`index.lock`).
