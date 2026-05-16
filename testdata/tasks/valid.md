---
generated_by: planner
generated_at: "2026-03-22T15:00:00Z"
dag:
  S01: []
  S02: [S01]
  S03: [S01]
  S04: [S02, S03]
---

## S01 — Foundation Tables ✅ PASSED

```yaml
type: database
```

### O que fazer
1. Criar migration create-tenants-table
2. Criar model tenant.model.js

### Critérios de sucesso
- [ ] `cd backend && npm run migrate` sem erros
- [ ] Models carregam no startup

### Arquivos
- **Criar:** `backend/src/migrations/001-create-tenants-table.js`
- **Criar:** `backend/src/models/tenant.model.js`
- **Modificar:** `backend/src/models/index.js`

---

## S02 — Tenant Context 🔄 RUNNING

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
1. Criar AsyncLocalStorage middleware
2. Implementar tenant resolution

### Critérios de sucesso
- [ ] Middleware injeta tenant no contexto
- [ ] Testes unitários passam

### Arquivos
- **Criar:** `backend/src/middleware/tenantContext.js`
- **Modificar:** `backend/src/app.js`

---

## S03 — CRM Migrations ⬜ PENDING

```yaml
type: database
depends_on: [S01]
```

### O que fazer
1. Adicionar tenant_id em tabelas CRM

### Critérios de sucesso
- [ ] Todas as migrations rodam sem erro

### Arquivos
- **Criar:** `backend/src/migrations/002-add-tenant-id.js`

---

## S04 — Integration Tests ❌ FAILED

```yaml
type: review
depends_on: [S02, S03]
```

### O que fazer
1. Escrever testes de integração

### Critérios de sucesso
- [ ] Coverage acima de 80%

### Arquivos
- **Criar:** `backend/src/__tests__/tenant.test.js`
