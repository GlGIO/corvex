---
generated_by: corvex-recipe:smoke-gate
dag:
    S01: []
    S02:
        - S01
---

## S01 — Approve deploy ✅ PASSED

```yaml
type: general
kind: human-gate
```

---

## S02 — Deploy ✅ PASSED

```yaml
type: general
kind: command
command: "echo \"deploying\""
depends_on: [S01]
```
