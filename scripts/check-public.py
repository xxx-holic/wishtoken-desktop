"""Check publishable Git files without printing potentially secret contents."""
from pathlib import Path
import argparse
import json
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
FORBIDDEN_PARTS = {'.handoff', '.codex', '.claude', 'node_modules', 'captures', 'sessions', 'reports', 'analysis'}
FORBIDDEN_NAMES = {'auth.json', 'accounts.json', 'config.json', 'bridge-identity.json', 'desktop.json', 'history.json', '.env'}
RULES = {
    'private key': re.compile(rb'-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----'),
    'JWT literal': re.compile(rb'eyJ[A-Za-z0-9_-]{25,}\.[A-Za-z0-9_-]{30,}\.[A-Za-z0-9_-]{15,}'),
    'secret token literal': re.compile(rb'(?:sk-(?:proj-)?|gh[pousr]_|github_pat_|rt_)[A-Za-z0-9_-]{25,}'),
    'personal Windows path': re.compile(rb'[CD]:[/\\](?:Users|Git_Project)[/\\](?:Mayn|rain|kawang|gptbridge|WishToApp)', re.I),
}

def secrets_from(samples):
    secrets = set()
    def walk(value):
        if isinstance(value, dict):
            for key, item in value.items():
                if key in ('access_token','refresh_token','id_token','api_key','password','seed','mfa_secret','totp_secret') and isinstance(item,str) and len(item)>12:
                    secrets.add(item.encode())
                else: walk(item)
        elif isinstance(value,list):
            for item in value: walk(item)
    for sample in samples:
        walk(json.loads(sample.read_text(encoding='utf-8-sig')))
    return secrets

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--sample',type=Path,action='append',default=[],help='Optional private credential files; only exact matching is performed')
    args=parser.parse_args()
    raw=subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard','-z'],cwd=ROOT)
    files=sorted(set(p.decode('utf-8') for p in raw.split(bytes([0])) if p))
    secrets=secrets_from(args.sample)
    findings=[]
    for rel in files:
        path=ROOT/rel
        parts=set(Path(rel).parts)
        if parts & FORBIDDEN_PARTS or path.name in FORBIDDEN_NAMES or path.name.startswith(('sub2api-account-','sub2api-rotation-')) or path.suffix.lower() in ('.jsonl','.log','.bak','.exe','.zip','.dmg','.pem','.key'):
            findings.append((rel,'private state or generated artifact path'))
        if path.is_symlink():
            findings.append((rel,'symlink requires manual publication review'));continue
        data=path.read_bytes()
        if any(token in data for token in secrets): findings.append((rel,'authorized sample credential found'))
        for label,pattern in RULES.items():
            if pattern.search(data): findings.append((rel,label))
        if path.suffix.lower() not in ('.png','.ico','.icns'):
            try:
                text=data.decode('utf-8')
                if chr(0xfffd) in text: findings.append((rel,'Unicode replacement character'))
            except UnicodeDecodeError: findings.append((rel,'unexpected non-UTF8 source file'))
    for rel,reason in findings: print(f'BLOCKED {rel}: {reason}')
    if findings: raise SystemExit(1)
    print(f'Public-source check passed: {len(files)} files, {len(secrets)} private credential values checked; no values printed.')

if __name__=='__main__': main()
