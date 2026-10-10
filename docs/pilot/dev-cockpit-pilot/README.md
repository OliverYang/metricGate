# Protocol v2 Pilot: dev-cockpit (Go Project)

## Overview
- **Target**: dev-cockpit (100% vibe-coded Go project)
- **Purpose**: Validate spec v1 clarity before cross-language expansion
- **Timeline**: 
  - Day 1 (2026-10-10): Task extraction + spec mapping
  - Day 2 (2026-10-11): Agent execution + result collection
  - Day 3 (2026-10-12): FP audit + spec v2 revision

## Success Criteria
- False-positive rate ≤5%
- Scorecard consumable by agent (clear remediation paths)
- All 10 tasks cover LC-101~404 clauses

## Tasks Summary
1. Add tab groups to editor → LC-201 (complexity), LC-301 (API stability)
2. Add worktree post scripts → LC-101 (repo knowledge), LC-401 (dependency management)
3. Force update check → LC-202 (error handling), LC-302 (version compatibility)
4. Add cost statistics → LC-102 (data integrity), LC-303 (external API usage)
5. Add Ollama launcher → LC-103 (environment setup), LC-402 (tool integration)
6. Add assistants to menu → LC-203 (UI consistency), LC-304 (user experience)
7. Add model choice → LC-204 (configuration), LC-403 (flexibility)
8. Fix editor tests → LC-104 (test coverage), LC-305 (regression prevention)
9. Link file paths in terminals → LC-205 (navigation), LC-404 (cross-module references)
10. Polish cost page → LC-206 (performance), LC-306 (visual feedback)

## Execution Notes
- Use existing Go parser (no TS support needed)
- Apply real policy.yaml from metricGate repo
- Record all findings, agent decisions, and remediation attempts
