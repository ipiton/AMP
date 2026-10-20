#!/usr/bin/env python3
"""Compute review-verdict.json from review-findings.md. Exit 0 only for gate=pass.

usage: verdict.py <branch> <commit> [--resolved R1,R2,...]
A finding counts as open fix-here unless listed in --resolved.
"""
import json, re, sys, datetime, pathlib

here = pathlib.Path(__file__).parent
branch, commit = sys.argv[1], sys.argv[2]
resolved = set()
if '--resolved' in sys.argv:
    resolved = set(sys.argv[sys.argv.index('--resolved') + 1].split(','))

text = (here / 'review-findings.md').read_text()
findings = re.findall(r'^### ([A-Z]+\d+) .*?\n- \*\*Severity:\*\* (\w+).*?\n- \*\*Disposition:\*\* ([^\n]+)', text, re.S | re.M)
counts = {'blocker': 0, 'major': 0, 'minor': 0, 'nit': 0}
open_blocker = major_nodisp = open_fix = 0
for fid, sev, disp in findings:
    counts[sev] += 1
    disp = disp.strip()
    # fix-here items scheduled for write-tests do not block the gate that unlocks write-tests
    fixed_here = 'fix-here' in disp
    if sev == 'major' and not disp:
        major_nodisp += 1
    if fixed_here and fid not in resolved and 'write-tests' not in disp:
        open_fix += 1
        if sev == 'blocker':
            open_blocker += 1

gate = 'pass' if open_blocker == 0 and major_nodisp == 0 and open_fix == 0 else 'fix_required'
verdict = {
    'gate': gate,
    'reason': None if gate == 'pass' else f'{open_fix} fix-here finding(s) open',
    'branch': branch,
    'commit': commit,
    'reviewed_at': datetime.date.today().isoformat(),
    'findings': counts,
    'open': {'blocker': open_blocker, 'major_without_disposition': major_nodisp},
}
(here / 'review-verdict.json').write_text(json.dumps(verdict, indent=2, ensure_ascii=False) + '\n')
print(json.dumps(verdict, ensure_ascii=False))
sys.exit(0 if gate == 'pass' else 1)
