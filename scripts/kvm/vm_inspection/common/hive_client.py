#!/usr/bin/env python3
"""Shared Hive validation and atomic JSON/file IO, independent of compute operations."""
import datetime as dt
import ipaddress
import json
import os
from pathlib import Path
import re
import tempfile
DEFAULT_POLICY = {'strong': {'action': 'pin'}, 'weak': {'action': 'pin'}}

def atomic_write(path, text, mode):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix='.' + path.name + '.', dir=path.parent)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as output:
            output.write(text)
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def utc():
    return dt.datetime.now(dt.timezone.utc).isoformat().replace('+00:00', 'Z')


def load(path, default=None):
    try:
        return json.loads(path.read_text())
    except (OSError, ValueError):
        return default


def save(path, value, mode=0o600):
    atomic_write(path, json.dumps(value, ensure_ascii=False, separators=(',', ':')) + '\n', mode)


def validate_policy(policy):
    if not isinstance(policy, dict) or set(policy) != {'strong', 'weak'}:
        raise ValueError('invalid match policy')
    for item in policy.values():
        if not isinstance(item, dict) or set(item) != {'action'} or item['action'] not in ('pin', 'none'):
            raise ValueError('invalid match policy action')
    return policy


def validate_snapshot(snapshot):
    if not isinstance(snapshot, dict) or type(snapshot.get('version')) is not int or snapshot['version'] < 0:
        raise ValueError('invalid snapshot version')
    fields = {'process': {'exe_path': str, 'cmdline_regex': str, 'parent_exe': str, 'uid': int, 'exe_deleted': bool},
              'file': {'path': str, 'sha256': str, 'size': int},
              'network': {'kind': str, 'value': str, 'port': int, 'proto': str}}
    for category, types in fields.items():
        rows = snapshot.get(category)
        if not isinstance(rows, list):
            raise ValueError('invalid snapshot category')
        ids = set()
        for row in rows:
            if not isinstance(row, dict) or type(row.get('id')) is not int or row['id'] < 1 or row['id'] in ids:
                raise ValueError('invalid feature identifier')
            ids.add(row['id'])
            if row.get('confidence') not in ('strong', 'weak') or not isinstance(row.get('family', ''), str):
                raise ValueError('invalid feature metadata')
            for key, kind in types.items():
                value = row.get(key)
                if value is not None and type(value) is not kind:
                    raise ValueError('invalid feature condition type')
            if category == 'process':
                if not row.get('exe_path') and not row.get('cmdline_regex'):
                    raise ValueError('empty process feature')
                if row.get('uid') is not None and row['uid'] < 0:
                    raise ValueError('invalid uid')
                re.compile(row.get('cmdline_regex') or '')
            elif category == 'file':
                if not row.get('path') and not row.get('sha256'):
                    raise ValueError('empty file feature')
                if row.get('sha256') and not re.fullmatch('[0-9a-f]{64}', row['sha256']):
                    raise ValueError('invalid feature hash')
                if row.get('size') is not None and row['size'] < 0:
                    raise ValueError('invalid file size')
            else:
                kind, value = row.get('kind'), row.get('value')
                if not value or kind not in ('ip', 'cidr', 'domain'):
                    raise ValueError('invalid network feature')
                if kind == 'ip':
                    ipaddress.ip_address(value)
                elif kind == 'cidr':
                    ipaddress.ip_network(value)
                if row.get('port') is not None and not 0 <= row['port'] <= 65535:
                    raise ValueError('invalid port')
                if row.get('proto') not in (None, '', 'tcp', 'udp'):
                    raise ValueError('invalid protocol')
    return snapshot
