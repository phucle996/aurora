# Frontend Docs Pipeline

Canonical FE docs pipeline:

1. `idea/` - Problem, context, solution direction, non-goals
2. `contract/` - Canonical FE contracts (page/component/state/api/design)
3. `spec/` - Feature/page behavior specs
4. `plan/` - Implementation plans
5. `review/` - Review checklist results and verdicts
6. `flow/` - Sequence/state/exception/retry flows
7. `runbook/` - Deploy/verify/debug/rollback/incidents

## Rule
- Downstream docs MUST consume upstream outputs.
- Backend API/auth/permission semantics remain source-of-truth for FE integration behavior.
