# metricGate CI/CD Integration

## Overview

metricGate integrates into CI/CD pipelines at multiple stages:

1. **Pre-commit hook** (already implemented) - Fast feedback on local changes
2. **PR gate** (GitHub Actions/GitLab CI) - Block merges with violations
3. **Scheduled full scan** - Track technical debt trends
4. **Dashboard & reporting** - Visualize quality metrics

## Quick Start

### Option 1: Use CI Helper Script (Recommended for MVP)

```bash
# JSON output for PR comments
./scripts/ci-helper.sh json findings.json .metricgate.yaml .

# HTML dashboard
./scripts/ci-helper.sh html dashboard.html .metricgate.yaml .
```

### Option 2: Direct CLI Usage

```bash
# Full repo analysis
./metricgate check . -format json -out findings.json

# Diff-only mode (for PRs)
./metricgate check . -diff -base HEAD~1 -format json -out findings.json

# SARIF output (for GitHub native integration)
./metricgate check . -format sarif -out findings.sarif
```

## GitHub Actions Integration

### PR Gate Workflow

See: `.github/workflows/metricgate-pr.yml`

```yaml
name: metricGate PR Gate

on:
  pull_request:
    branches: [main, master]

jobs:
  metricgate-check:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    
    - name: Set up Go
      uses: actions/setup-go@v5
      with:
        go-version: '1.23'
        
    - name: Install metricGate
      run: cd .. && go build -o metricgate ./cmd/metricgate
      
    - name: Run checks
      run: |
        ./metricgate check . -diff -base ${{ github.base_ref }} \
          -format json -out findings.json
          
    - name: Upload findings
      uses: actions/upload-artifact@v4
      with:
        name: metricgate-findings
        path: findings.json
        
    - name: Comment on PR
      uses: peter-evans/create-issue-comment@v4
      with:
        repository: ${{ github.repository }}
        issue-number: ${{ github.event.pull_request.number }}
        body-file: findings-summary.txt
```

### Scheduled Full Scan

See: `.github/workflows/metricgate-schedule.yml`

Runs daily at 2 AM UTC to track technical debt trends.

## GitLab CI Integration

See: `.gitlab-ci.yml`

```yaml
metricgate-pr-check:
  stage: check
  image: golang:1.23
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
  script:
    - cd .. && go build -o metricgate ./cmd/metricgate
    - ./metricgate check . -diff -base $CI_MERGE_REQUEST_TARGET_BRANCH_NAME \
        -format json -out findings.json
  artifacts:
    reports:
      sast: findings.sarif
```

## Dashboard Generation

### HTML Dashboard

```bash
./scripts/ci-helper.sh html dashboard.html .metricgate.yaml .
```

Generates a self-contained HTML file with:
- Summary statistics (files, errors, warnings)
- Table of all findings
- Severity breakdown

### SARIF Output (GitHub Native)

```bash
./metricgate check . -format sarif -out findings.sarif
```

GitHub automatically displays SARIF results in:
- PR checks tab
- Code scanning alerts
- Dependency graph

## Trend Tracking

### Historical Data

metricGate stores results in `.metricgate/history.db`:

```sql
-- Query violation trends
SELECT 
    DATE(run.started_at) as date,
    COUNT(CASE WHEN f.severity='error' THEN 1 END) as errors,
    COUNT(CASE WHEN f.severity='warning' THEN 1 END) as warnings
FROM runs run
LEFT JOIN findings f ON f.run_id = run.id
GROUP BY DATE(run.started_at)
ORDER BY date DESC;
```

### Metric Distributions

```bash
./metricgate stats . > metrics-trend.json
```

Outputs p50/p90/p95/max for all complexity metrics.

## Best Practices

### 1. Gradual Adoption

Start with warning-level only:

```yaml
# In .metricgate.yaml
rules:
  fail_on: warning  # Change to error after stabilization
```

### 2. Threshold Calibration

Use human code baseline:

```bash
# Measure current human code distribution
./metricgate stats . > human-baseline.json

# Set thresholds above 95th percentile
# Then tighten gradually over time
```

### 3. False Positive Management

Monitor FP rate and adjust:

```yaml
# Disable noisy rules during observation period
rules:
  cyclomatic-complexity:
    enabled: false  # Re-enable after threshold calibration
```

### 4. Architecture Drift Detection

```bash
# Initial snapshot
./metricgate arch snapshot -out arch.json

# In PR check
./metricgate check . -diff -arch-base arch.json
```

## Troubleshooting

### Issue: "no policy file at .metricgate.yaml"

**Solution**: Pass explicit config path:

```bash
./metricgate check . -config /path/to/.metricgate.yaml
```

### Issue: Slow execution on large repos

**Solution**: Use diff-mode only:

```bash
./metricgate check . -diff -base HEAD~1
```

### Issue: Too many violations blocking PRs

**Solution**: Start with `fail_on: warning`, then gradually enforce:

```yaml
rules:
  fail_on: warning  # First stabilize
  # Later:
  fail_on: error    # Then enforce strictly
```

## Next Steps

1. **Implement web dashboard** (P2) - Visual trend charts
2. **Add Slack/Teams notifications** (P2) - Real-time alerts
3. **Auto-threshold calibration** (P2) - ML-based suggestions
4. **IDE LSP server** (P3) - Real-time inline feedback

## Resources

- GitHub Actions workflow: `.github/workflows/metricgate-pr.yml`
- GitLab CI pipeline: `.gitlab-ci.yml`
- CI helper script: `scripts/ci-helper.sh`
- Design doc: `docs/design/ci-cd-integration-design.md` (TBD)
