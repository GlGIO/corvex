I have enough context. Writing the tasks.md now.I've generated the complete `tasks.md` for the CH-03 handoff/anchor-contract batch. It decomposes the spec into 5 tasks with a clean DAG:

- **S01** (backend) — Git changed-files helper in `internal/recovery` (Objective A). Independent.
- **S02** (backend) — TASK-REPORT contract in `buildWorkerPrompt` + `parseTaskReport` parser + tests (Objective B parser/contract half). Independent.
- **S03** (backend, ←S01,S02) — Wire the git helper + parser into the `executeTask` PASS branch to populate real files, Summary, Decisions, NextTaskContext. Report still optional here (keeps existing tests green).
- **S04** (backend, ←S03) — Objective C: missing TASK-REPORT/HANDOFF becomes a retried task-level failure, with the last-task exception; updates stub providers so PASS-path tests stay green.
- **S05** (review, ←S04) — End-to-end validation that the anchor is fully populated.

**DAG shape:** S01/S02 run in parallel → S03 joins them → S04 → S05.

Key design choices worth flagging:
- **The git-diff-before-commit ordering** (capture changed files *before* `MarkCheckpoint`) is the highest-risk subtlety — I quoted it verbatim and added anti-padrão in both S01 and S03.
- **"Missing block" vs "empty HANDOFF"** are distinguished in the parser (S02 returns a found/not-found signal) because S04's last-task exception depends on telling them apart — empty HANDOFF is allowed only when `NextTask == ""`, but an absent block never is.
- **Report-optional in S03, mandatory in S04** — this split keeps each task's `go test ./...` green: S03 lands the populate logic with graceful fallback, then S04 flips on the mandatory guard *and* updates the stubs in the same task so the tree stays buildable.
- The exact diagnosis string and the TASK-REPORT field labels are quoted verbatim to avoid the reviewer's `ambiguity` failure.