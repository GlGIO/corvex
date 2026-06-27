---
generated_by: corvex-recipe:smoke-loop
dag:
    S01: []
---

## S01 — Retry until ready ✅ PASSED

```yaml
type: general
kind: command
command: "c=$(cat /tmp/corvex_ctr 2>/dev/null||echo 0);c=$((c+1));echo $c>/tmp/corvex_ctr;echo attempt $c;[ $c -ge 2 ]"
loop_max: 4
```
