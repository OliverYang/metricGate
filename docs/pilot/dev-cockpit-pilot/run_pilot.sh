#!/bin/bash
# Protocol v2 Pilot Execution Script
# Target: dev-cockpit (Go project)
# Date: 2026-10-11

set -e

SRC_DIR="/tmp/m2b_sandbox"
METRICGATE="/Users/mac/Downloads/metricGate/metricgate"
OUT_DIR="/tmp/m2b_runs/out_devcockpit_pilot"

mkdir -p "$OUT_DIR"

echo "=== Protocol v2 Pilot: dev-cockpit ==="
echo "Start time: $(date)"
echo ""

# Clone/clean sandbox
rm -rf "$SRC_DIR"
git clone --depth 50 https://github.com/marein/dev-cockpit.git "$SRC_DIR" 2>&1 | tail -3

cd "$SRC_DIR"

# Build metricGate
cd /Users/mac/Downloads/metricGate
go build -o metricgate ./cmd/metricgate
cp metricgate "$SRC_DIR/"

# Run each task
for i in $(seq -w 1 10); do
    TASK_FILE="task_$(printf "%d" $i).txt"
    echo "=========================================="
    echo "Running Task $i: $TASK_FILE"
    echo "=========================================="
    
    # Extract task description from file
    TASK_DESC=$(grep -A 5 "^## Agent Instructions" "../$TASK_FILE" | tail -4 | tr -d '# ')
    
    # Run agent with task
    cd "$SRC_DIR"
    pi --provider 2api --model glm-5.3-flash -p "Implement: $TASK_DESC" \
      --timeout 1800 \
      --output "$OUT_DIR/task_$i_result.json" 2>&1 | tee "$OUT_DIR/task_$i.log"
    
    # Check results
    if [ -f "$OUT_DIR/task_$i_result.json" ]; then
        METRICGATE check . -format json > "$OUT_DIR/task_$i_findings.json"
        echo "Task $i completed. Findings:"
        cat "$OUT_DIR/task_$i_findings.json" | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'  Errors: {d.get(\"error_count\",0)}, Warnings: {d.get(\"warning_count\",0)}')"
    else
        echo "Task $i FAILED - no output"
    fi
    
    echo ""
done

echo ""
echo "=== Pilot Complete ==="
echo "End time: $(date)"
echo "Results in: $OUT_DIR"
