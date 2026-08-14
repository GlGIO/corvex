package orchestrator

// taskReportBlock is appended to mock Worker outputs so they satisfy the
// TASK-REPORT/HANDOFF contract enforced by executeTask (CH-03).
const taskReportBlock = "\n\nTASK-REPORT:\nSUMMARY: implemented the task.\nDECISIONS:\n- took the straightforward approach\nHANDOFF: state is ready for the next task"

const testTasksMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
  S02: [S01]
---

## S01 — First Task ⬜ PENDING

` + "```yaml\n" +
	`type: general
depends_on: []
` + "```\n" + `
### O que fazer
Do the first thing.

### Critérios de sucesso
- [ ] First criterion passes

### Arquivos
- **Criar:** ` + "`test-file-1.txt`" + `

---

## S02 — Second Task ⬜ PENDING

` + "```yaml\n" +
	`type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
Do the second thing.

### Critérios de sucesso
- [ ] Second criterion passes

### Arquivos
- **Criar:** ` + "`test-file-2.txt`" + `
`

// dagViolationTasksMD models the show-lqip-buffer corruption: S02 is PASSED but
// depends on S01 which is still PENDING. The executor must refuse to run.
const dagViolationTasksMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
  S02: [S01]
---

## S01 — First Task ⬜ PENDING

` + "```yaml\n" +
	`type: general
depends_on: []
` + "```\n" + `
### O que fazer
Do the first thing.

---

## S02 — Second Task ✅ PASSED

` + "```yaml\n" +
	`type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
Do the second thing.
`

// runningOnResumeTasksMD models an interrupted previous run: S01 was left in
// RUNNING. The executor must reset it to PENDING and re-pick it.
const runningOnResumeTasksMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
---

## S01 — First Task 🔄 RUNNING

` + "```yaml\n" +
	`type: general
depends_on: []
` + "```\n" + `
### O que fazer
Do the first thing.

### Critérios de sucesso
- [ ] First criterion passes

### Arquivos
- **Criar:** ` + "`test-file-1.txt`" + `
`

const allPassedTasksMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
  S02: [S01]
---

## S01 — First Task ✅ PASSED

` + "```yaml\n" +
	`type: general
depends_on: []
` + "```\n" + `
### O que fazer
Do the first thing.

### Critérios de sucesso
- [ ] First criterion passes

---

## S02 — Second Task ✅ PASSED

` + "```yaml\n" +
	`type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
Do the second thing.

### Critérios de sucesso
- [ ] Second criterion passes
`

// diamondTasksMD models a diamond DAG: S01 → {S02, S03} → S04. S02 and S03 are
// independent of each other; S04 depends on both. Used to prove that a failure
// in S02 skips only S04 (its dependent) while S03 still runs.
const diamondTasksMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
  S02: [S01]
  S03: [S01]
  S04: [S02, S03]
---

## S01 — Root ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: []
` + "```\n" + `
### O que fazer
Root task.

---

## S02 — Left (will fail) ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
Left branch.

---

## S03 — Right (independent) ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
Right branch.

---

## S04 — Join (depends on both) ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S02, S03]
` + "```\n" + `
### O que fazer
Join branch.
`

// fanOutTasksMD: S01 root, then S02/S03/S04 all depend only on S01 — a single
// wide level that exercises the parallel scheduler.
const fanOutTasksMD = `---
generated_by: test
dag:
  S01: []
  S02: [S01]
  S03: [S01]
  S04: [S01]
---

## S01 — Root ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: []
` + "```\n" + `
### O que fazer
Root.

---

## S02 — A ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
A.

---

## S03 — B ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
B.

---

## S04 — C ⬜ PENDING

` + "```yaml\n" + `type: general
depends_on: [S01]
` + "```\n" + `
### O que fazer
C.
`

func commandTasksMD(cmd1, cmd2 string) string {
	return "---\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — Gate ⬜ PENDING\n\n```yaml\ntype: general\nkind: command\ncommand: " + cmd1 + "\n```\n\n### O que fazer\ngate\n\n---\n\n" +
		"## S02 — After ⬜ PENDING\n\n```yaml\ntype: general\nkind: command\ncommand: " + cmd2 + "\ndepends_on: [S01]\n```\n\n### O que fazer\nafter\n"
}

func gateTasksMD() string {
	return "---\ndag:\n  S01: []\n  S02: [S01]\n---\n\n" +
		"## S01 — Approve release ⬜ PENDING\n\n```yaml\ntype: general\nkind: human-gate\n```\n\n### O que fazer\ngate\n\n---\n\n" +
		"## S02 — Ship ⬜ PENDING\n\n```yaml\ntype: general\nkind: command\ncommand: \"true\"\ndepends_on: [S01]\n```\n\n### O que fazer\nship\n"
}

func loopTaskMD(yamlExtra string) string {
	return "---\ndag:\n  S01: []\n---\n\n" +
		"## S01 — Loop ⬜ PENDING\n\n```yaml\ntype: general\nkind: command\n" + yamlExtra + "```\n\n### O que fazer\nloop\n"
}

const singleTaskMD = `---
generated_by: test
generated_at: "2026-01-01T00:00:00Z"
dag:
  S01: []
---

## S01 — Only Task ⬜ PENDING

` + "```yaml\n" +
	`type: general
depends_on: []
` + "```\n" + `
### O que fazer
Do the thing.

### Critérios de sucesso
- [ ] Criterion passes

### Arquivos
- **Criar:** ` + "`out.txt`" + `
`
