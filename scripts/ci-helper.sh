#!/bin/bash
# metricGate CI Helper - SonarQube-style HTML report
# Usage: ci-helper.sh html <output.html> [policy] [path]
set -e

FORMAT="${1:-html}"
OUTPUT="${2:-dashboard.html}"
POLICY="${3:-policy.yaml}"
PATH_ARG="${4:-.}"

TMPJSON=$(mktemp /tmp/mg-findings.XXXXXX.json)
TMPDIFF=$(mktemp /tmp/mg-diff.XXXXXX.json)
trap 'rm -f "$TMPJSON" "$TMPDIFF"' EXIT

echo "Running metricGate check..." >&2
./metricgate check "$PATH_ARG" -config "$POLICY" -format json > "$TMPJSON" 2>/dev/null || true

# Diff-mode pass for "new code" column (best effort)
echo "Running diff-mode check (new code)..." >&2
./metricgate check "$PATH_ARG" -config "$POLICY" -format json -diff -base HEAD > "$TMPDIFF" 2>/dev/null || true

OUT="$OUTPUT" TMPJSON="$TMPJSON" TMPDIFF="$TMPDIFF" SCANPATH="$PATH_ARG" python3 <<'PY'
import json, os, html as h
from datetime import datetime

data = json.load(open(os.environ['TMPJSON']))
try:
    diff = json.load(open(os.environ['TMPDIFF']))
except Exception:
    diff = {}

findings = data.get('findings', []) or []
dfindings = diff.get('findings', []) or []
errors = sum(1 for x in findings if x.get('severity') == 'error')
warns  = sum(1 for x in findings if x.get('severity') == 'warning')
derrors = sum(1 for x in dfindings if x.get('severity') == 'error')
dwarns  = sum(1 for x in dfindings if x.get('severity') == 'warning')
gate_pass = errors == 0

# --- rating (A-E, SonarQube style): map error+warning density to a grade
files = max(data.get('files', 0), 1)
density = (errors * 10 + warns) / files
def rating(n):
    return 'A' if n < 0.1 else 'B' if n < 0.5 else 'C' if n < 1.5 else 'D' if n < 4 else 'E'
maint_rating = rating(density)
sec_rating = rating(sum(1 for x in findings if x.get('ruleId','').startswith(('banned','unknown')))/files)
rel_rating = rating(sum(1 for x in findings if 'error' in (x.get('severity') or '') )/files * 10)

# --- rule tally with bars
tally = {}
sev_by_rule = {}
for x in findings:
    r = x.get('ruleId', '?')
    tally[r] = tally.get(r, 0) + 1
    sev_by_rule.setdefault(r, set()).add(x.get('severity'))
max_tally = max(tally.values()) if tally else 1
tally_rows = ''
for k, v in sorted(tally.items(), key=lambda kv: -kv[1]):
    has_err = 'error' in sev_by_rule[k]
    dot = '<span class="dot err"></span>' if has_err else '<span class="dot wrn"></span>'
    pct = int(v / max_tally * 100)
    tally_rows += ('<tr data-rule="%s" style="cursor:pointer"><td>%s%s</td><td class="num">%d</td>'
                   '<td><div class="bar"><div class="fill %s" style="width:%d%%"></div></div></td></tr>'
                   % (h.escape(k), dot, h.escape(k), v,
                      'f-err' if has_err else 'f-wrn', pct))

# --- findings table (grouped by rule, collapsible)
def esc(s): return h.escape(str(s or ''))
by_rule = {}
for x in findings:
    by_rule.setdefault(x.get('ruleId','?'), []).append(x)

rule_blocks = ''
for r in sorted(by_rule, key=lambda k: (len(by_rule[k]), k), reverse=True):
    items = by_rule[r]
    has_err = any(x.get('severity')=='error' for x in items)
    rows = ''
    for x in items:
        sev = x.get('severity','')
        rows += ('<tr class="%s"><td class="num">%s</td><td class="num">%d</td><td>%s</td></tr>'
                 % ('err' if sev=='error' else 'wrn' if sev=='warning' else '',
                    esc(x.get('file','')), x.get('startLine',0) or 0, esc(x.get('message',''))))
    rule_blocks += ('''<details class="rule-group" open>
<summary><span class="dot %s"></span>%s <span class="cnt">%d</span></summary>
<table><tr><th>File</th><th>Line</th><th>Message</th></tr>%s</table></details>'''
                    % ('err' if has_err else 'wrn', esc(r), len(items), rows))

# --- gate banner fail details
if gate_pass:
    banner = '<div class="gate pass"><span class="gate-ico">&#10003;</span> Quality Gate: PASSED</div>'
else:
    conds = '<div class="cond"><b>%d</b> error-level findings <span class="vs">required: 0</span></div>' % errors
    banner = ('<div class="gate fail"><span class="gate-ico">&#10007;</span> Quality Gate: FAILED'
              '<div class="conds">%s</div></div>' % conds)

RATING_CLS = {'A':'rA','B':'rB','C':'rC','D':'rD','E':'rE'}

html = '''<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>metricGate — Quality Report</title>
<style>
:root { --bg:#f4f6f9; --card:#fff; --ink:#243241; --mut:#7a8699; --line:#e4e8ee;
        --red:#d32f2f; --org:#ef6c00; --grn:#2e7d32; --blu:#1565c0; --yel:#f9e6b0 }
* { box-sizing:border-box }
body { margin:0; font-family:-apple-system,"Segoe UI",Roboto,Arial,sans-serif; background:var(--bg); color:var(--ink); font-size:14px }
.wrap { max-width:1200px; margin:0 auto; padding:24px 20px 60px }
h1 { font-size:20px; margin:0 0 4px } .sub { color:var(--mut); font-size:12.5px; margin-bottom:18px }

/* quality gate banner */
.gate { border-radius:8px; padding:18px 22px; margin-bottom:20px; color:#fff; font-size:17px; font-weight:600;
        display:flex; align-items:center; gap:12px }
.gate.pass { background:linear-gradient(90deg,#2e7d32,#43a047) }
.gate.fail { background:linear-gradient(90deg,#c62828,#e53935); display:block }
.gate-ico { display:inline-flex; width:30px; height:30px; border-radius:50%%; background:rgba(255,255,255,.25);
            align-items:center; justify-content:center; font-size:16px }
.conds { margin-top:10px; font-weight:400; font-size:13.5px }
.cond { background:rgba(0,0,0,.14); border-radius:6px; padding:8px 12px; margin-top:6px }
.vs { opacity:.85; font-size:12.5px; margin-left:8px }

/* ratings row */
.ratings { display:grid; grid-template-columns:repeat(3,1fr); gap:14px; margin-bottom:20px }
.rcard { background:var(--card); border:1px solid var(--line); border-radius:8px; padding:16px 18px; display:flex; align-items:center; gap:16px }
.grade { width:52px; height:52px; border-radius:8px; color:#fff; font-size:28px; font-weight:800;
         display:flex; align-items:center; justify-content:center }
.rA { background:#2e7d32 } .rB { background:#9ccc65 } .rC { background:#fdd835; color:#5d4037 }
.rD { background:#ef6c00 } .rE { background:#c62828 }
.rlabel { font-weight:600; margin-bottom:2px } .rval { color:var(--mut); font-size:12.5px }

/* stat tiles */
.tiles { display:grid; grid-template-columns:repeat(4,1fr); gap:14px; margin-bottom:22px }
.tile { background:var(--card); border:1px solid var(--line); border-radius:8px; padding:14px 18px }
.tile .v { font-size:26px; font-weight:700 }
.tile .l { color:var(--mut); font-size:12px; text-transform:uppercase; letter-spacing:.4px }
.tile.err .v { color:var(--red) } .tile.wrn .v { color:var(--org) } .tile.new .v { color:var(--blu) }

/* columns: overall vs new code */
.cols { display:grid; grid-template-columns:1fr 1fr; gap:14px; margin-bottom:24px }
.col { background:var(--card); border:1px solid var(--line); border-radius:8px; overflow:hidden }
.col > h3 { margin:0; padding:10px 16px; font-size:13.5px; border-bottom:1px solid var(--line) }
.col.new > h3 { background:var(--yel) }
.col table { width:100%%; border-collapse:collapse }
.col td { padding:9px 16px; border-top:1px solid var(--line) }
.col td:first-child { color:var(--mut) } .col td:last-child { text-align:right; font-weight:700 }

/* rule distribution */
.panel { background:var(--card); border:1px solid var(--line); border-radius:8px; padding:16px 18px; margin-bottom:24px }
.panel h2 { font-size:15px; margin:0 0 12px }
table.dist { width:100%%; border-collapse:collapse }
.dist td { padding:5px 8px; border-top:1px solid var(--line); font-size:13px }
.dist tr:first-child td { border-top:none }
.dist td.num { text-align:right; width:46px; font-weight:600 }
.bar { background:#edf0f4; height:10px; border-radius:5px; width:100%%; min-width:180px }
.fill { height:10px; border-radius:5px }
.f-err { background:var(--red) } .f-wrn { background:#f0a030 }
.dot { display:inline-block; width:9px; height:9px; border-radius:50%%; margin-right:8px }
.dot.err { background:var(--red) } .dot.wrn { background:#f0a030 }

/* findings groups */
details.rule-group { border:1px solid var(--line); border-radius:8px; margin-bottom:10px; background:var(--card); overflow:hidden }
details.rule-group summary { padding:11px 16px; cursor:pointer; font-weight:600; font-size:13.5px;
                             display:flex; align-items:center; list-style:none }
details.rule-group summary::-webkit-details-marker { display:none }
.cnt { margin-left:auto; background:#edf0f4; border-radius:12px; padding:2px 10px; font-size:12px }
.rule-group table { width:100%%; border-collapse:collapse }
.rule-group th { background:#fafbfc; text-align:left; padding:7px 16px; font-size:11.5px; color:var(--mut);
                 text-transform:uppercase; border-top:1px solid var(--line) }
.rule-group td { padding:7px 16px; border-top:1px solid var(--line); font-size:12.5px }
.rule-group tr.err td:first-child { color:var(--red); font-weight:600 }
.rule-group tr.wrn td:first-child { color:var(--org) }
.rule-group td.msg { color:#44505f }

footer { color:var(--mut); font-size:11.5px; margin-top:28px; text-align:center }
</style></head><body><div class="wrap">
<h1>Quality Report</h1>
<div class="sub">metricGate &middot; %s &middot; %s &middot; policy: %s</div>

%s

<div class="ratings">
 <div class="rcard"><div class="grade %s">%s</div><div><div class="rlabel">Reliability</div><div class="rval">%d error-level findings</div></div></div>
 <div class="rcard"><div class="grade %s">%s</div><div><div class="rlabel">Security &amp; API</div><div class="rval">banned-api / unknown-dep findings</div></div></div>
 <div class="rcard"><div class="grade %s">%s</div><div><div class="rlabel">Maintainability</div><div class="rval">%d warnings &middot; density %.2f/file</div></div></div>
</div>

<div class="tiles">
 <div class="tile"><div class="v">%d</div><div class="l">Files scanned</div></div>
 <div class="tile err"><div class="v">%d</div><div class="l">Errors</div></div>
 <div class="tile wrn"><div class="v">%d</div><div class="l">Warnings</div></div>
 <div class="tile new"><div class="v">%d</div><div class="l">New (diff vs HEAD)</div></div>
</div>

<div class="cols">
 <div class="col"><h3>Overall</h3><table>
   <tr><td>Errors</td><td>%d</td></tr>
   <tr><td>Warnings</td><td>%d</td></tr>
   <tr><td>Suppressed</td><td>%d</td></tr></table></div>
 <div class="col new"><h3>New Code (diff vs HEAD)</h3><table>
   <tr><td>Errors</td><td>%d</td></tr>
   <tr><td>Warnings</td><td>%d</td></tr></table></div>
</div>

<div class="panel"><h2>Findings by Rule</h2><table class="dist">%s</table></div>

<h2 style="font-size:15px;margin:0 0 12px">All Findings (%d)</h2>
%s

<footer>metricGate deterministic gate &middot; byte-identical verdicts &middot; same policy drives pre-gen digest &amp; post-gen check</footer>
</div></body></html>''' % (
    esc(os.environ.get('SCANPATH','.')),
    datetime.now().strftime('%Y-%m-%d %H:%M'),
    esc(os.path.basename(os.environ.get('POLICY','policy.yaml'))),
    banner,
    RATING_CLS[rel_rating], rel_rating, errors,
    RATING_CLS[sec_rating], sec_rating,
    RATING_CLS[maint_rating], maint_rating, warns, density,
    data.get('files', 0), errors, warns, len(dfindings),
    errors, warns, data.get('ignored', 0),
    derrors, dwarns,
    tally_rows,
    len(findings), rule_blocks)

open(os.environ['OUT'],'w').write(html)
print('OK %s (%d errors, %d warnings, %d new-in-diff)' % (os.environ['OUT'], errors, warns, len(dfindings)))
PY
