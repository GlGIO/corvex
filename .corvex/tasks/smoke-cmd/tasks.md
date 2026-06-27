---
generated_by: corvex-recipe:smoke-cmd
dag:
    S01: []
    S02:
        - S01
---

## S01 — Say hi ✅ PASSED

```yaml
type: general
kind: command
command: "echo \"hello from command stage\""
```

---

## S02 — Build check ✅ PASSED

```yaml
type: general
kind: command
command: "go build ./..."
depends_on: [S01]
```
