#!/usr/bin/env python3
"""IAM-only operator client. Secret A stays in a private local session file."""
import argparse
import base64
from datetime import datetime, timezone
import getpass
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile


def random_id():
    return base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip('=')


def invoke(function, payload):
    with tempfile.TemporaryDirectory(prefix='email-verification-') as directory:
        request = Path(directory) / 'request.json'
        response = Path(directory) / 'response.json'
        request.write_text(json.dumps(payload))
        request.chmod(0o600)
        result = subprocess.run(['aws', 'lambda', 'invoke', '--function-name', function,
                                 '--invocation-type', 'RequestResponse', '--payload',
                                 'fileb://' + str(request), str(response)],
                                capture_output=True, text=True)
        if result.returncode:
            raise RuntimeError('AWS invocation failed. Check your profile, region and InvokeFunction permission.')
        if json.loads(result.stdout).get('FunctionError'):
            raise RuntimeError('Test host failed. Check its CloudWatch logs; no request secrets are logged by the host.')
        return json.loads(response.read_text())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['start', 'confirm', 'status', 'tick'])
    parser.add_argument('--function', required=True, help='API function for start/confirm/status; worker function for tick')
    parser.add_argument('--email', help='Configured test recipient; required for start')
    parser.add_argument('--session', type=Path, default=Path('.testhost/session.json'))
    args = parser.parse_args()
    if args.action == 'tick':
        print(json.dumps(invoke(args.function, {'action': 'tick'})))
        return
    if args.action == 'start' and not args.session.exists():
        if not args.email:
            parser.error('--email is required for start')
        secret = random_id()
        session = {'function': args.function, 'email': args.email, 'operation': random_id(),
                   'secret': secret, 'authorized_at': datetime.now(timezone.utc).isoformat(),
                   'challenge': base64.urlsafe_b64encode(hashlib.sha256(secret.encode()).digest()).decode().rstrip('=')}
        args.session.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        # Never overwrite an existing authorization or reset its original deadline.
        fd = os.open(args.session, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as handle:
            json.dump(session, handle)
    if args.session.is_symlink() or not args.session.is_file():
        parser.error('Use an existing regular session file, or start a new flow.')
    if os.name == 'posix' and args.session.stat().st_mode & 0o077:
        parser.error('Session file must be private: chmod 600 SESSION')
    session = json.loads(args.session.read_text())
    if session['function'] != args.function or (args.email and session['email'] != args.email):
        parser.error('Session belongs to another function or recipient. Use a new session path.')
    payload = {'action': args.action, 'operation': session['operation']}
    if args.action == 'start':
        payload.update({key: session[key] for key in ['email', 'challenge', 'authorized_at']})
    elif args.action == 'confirm':
        payload.update(secret=session['secret'], code=getpass.getpass('Email code: ').strip())
    print(json.dumps(invoke(args.function, payload)))


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, OSError, ValueError, KeyError) as error:
        raise SystemExit(str(error))
