#!/usr/bin/env python3
"""Node-local Hive configuration, last successful snapshot and latest health."""
import base64
import datetime as dt
import email.utils
import fcntl
import grp
import ipaddress
import json
import os
from pathlib import Path
import random
import re
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
from metrics import atomic_write

CONFIG = Path('/etc/sysconfig/cloudland-hive.json')
CA = Path('/etc/sysconfig/cloudland-hive-ca.pem')
ROOT = Path(os.environ.get('cache_dir', '/opt/cloudland/cache')) / 'vm_inspection'
RUN = Path(os.environ.get('run_dir', '/opt/cloudland/run')) / 'vm_inspection'
INTERVAL = 300  # Refresh about every five minutes.
JITTER = 60  # Spread requests across nodes.
DEFAULT_POLICY = {'strong': {'action': 'pin'}, 'weak': {'action': 'pin'}}


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


def cached():
    try:
        return validate_snapshot(load(ROOT / 'hive_snapshot.json'))
    except (ValueError, TypeError, re.error):
        return None


class NoRedirect(urllib.request.HTTPRedirectHandler):
    # Never forward the read token to a redirect destination.
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def refresh(config, status, force=False):
    snapshot = cached()
    status.update(config_updated_at=config.get('updated_at', ''), using_cache=snapshot is not None,
                  snapshot_version=snapshot['version'] if snapshot else None)
    if not config.get('url'):
        status.update(fetch_status='not_configured', using_cache=False, error_code='', error_message='')
        return status
    next_refresh = status.get('next_refresh_epoch', status.get('attempt_epoch', 0) + INTERVAL)
    if not force and time.time() < next_refresh:
        return status
    status['next_refresh_epoch'] = int(time.time()) + INTERVAL + random.randint(0, JITTER)
    for attempt in range(4):
        status.update(last_attempt_at=utc(), attempt_epoch=int(time.time()))
        save(ROOT / 'hive_status.json', status)
        wait = min(300, 5 * (2 ** attempt))
        retry = True
        try:
            context = ssl.create_default_context(cafile=str(CA) if CA.exists() else None)
            headers = {'Authorization': 'Bearer ' + config.get('token', '')}
            if snapshot is not None and status.get('etag_url') == config['url'] and status.get('etag'):
                headers['If-None-Match'] = status['etag']
            request = urllib.request.Request(config['url'].rstrip('/') + '/v1/snapshot', headers=headers)
            opener = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPSHandler(context=context))
            try:
                response = opener.open(request, timeout=10)
            except urllib.error.HTTPError as error:
                if error.code != 304 or snapshot is None:
                    raise
                response = error
            with response:
                code = response.code
                try:
                    expiry = response.fp.raw._sock.getpeercert().get('notAfter')
                    if expiry:
                        status['certificate_expires_at'] = dt.datetime.fromtimestamp(ssl.cert_time_to_seconds(expiry), dt.timezone.utc).isoformat().replace('+00:00', 'Z')
                except (AttributeError, OSError, ValueError):
                    pass
                if code == 200:
                    body = response.read(1024 * 1024 + 1)
                    if len(body) > 1024 * 1024:
                        raise ValueError('snapshot exceeds 1 MiB')
                    replacement = validate_snapshot(json.loads(body))
                    save(ROOT / 'hive_snapshot.json', replacement)
                    snapshot = replacement
                    status.update(etag=response.headers.get('ETag', ''), etag_url=config['url'])
                elif code != 304:
                    raise ValueError('unexpected HTTP response')
            status.update(fetch_status='ok', last_success_at=utc(), snapshot_version=snapshot['version'],
                          using_cache=False, error_code='', error_message='')
            break
        except urllib.error.HTTPError as error:
            status.update(error_code='http_' + str(error.code), error_message='Hive HTTP ' + str(error.code))
            retry = error.code == 429 or error.code >= 500
            if error.code == 429:
                retry_after = error.headers.get('Retry-After', '60')
                try:
                    wait = min(300, max(60, float(retry_after)))
                except ValueError:
                    try:
                        wait = min(300, max(60, email.utils.parsedate_to_datetime(retry_after).timestamp() - time.time()))
                    except (ValueError, TypeError):
                        wait = 60
        except (ssl.SSLError, urllib.error.URLError, TimeoutError, OSError) as error:
            reason = getattr(error, 'reason', error)
            cert = isinstance(reason, ssl.SSLCertVerificationError)
            status.update(error_code='certificate_error' if cert else 'connection_failed',
                          error_message='Hive certificate verification failed' if cert else 'Hive connection failed or timed out')
            retry = not cert
        except (ValueError, TypeError, re.error):
            status.update(error_code='invalid_snapshot', error_message='Hive snapshot validation failed')
        status.update(fetch_status='failed', using_cache=snapshot is not None)
        save(ROOT / 'hive_status.json', status)
        if attempt < 3 and retry:
            time.sleep(wait)
        else:
            break
    return status


def sync_config(encoded, status):
    try:
        config = json.loads(base64.b64decode(encoded, validate=True))
        stamp = config['updated_at']
        dt.datetime.fromisoformat(stamp.replace('Z', '+00:00'))
        if load(CONFIG, {}).get('updated_at') and dt.datetime.fromisoformat(load(CONFIG)['updated_at'].replace('Z', '+00:00')) > dt.datetime.fromisoformat(stamp.replace('Z', '+00:00')):
            return load(CONFIG), status, False
        url, token = config['url'].strip(), config['token']
        parsed = urllib.parse.urlsplit(url)
        if url and (parsed.scheme != 'https' or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment):
            raise ValueError('invalid Hive URL')
        if len(url) > 512 or len(token) > 4096 or any(c.isspace() for c in token):
            raise ValueError('invalid Hive credentials')
        save(CONFIG, {'url': url, 'token': token, 'updated_at': stamp}, 0o640)
        if os.geteuid() == 0:
            os.chown(CONFIG, 0, grp.getgrnam('cland').gr_gid)
        status.update(sync_status='ok', config_updated_at=stamp, error_code='', error_message='')
        return load(CONFIG), status, True
    except (ValueError, KeyError, TypeError, OSError, ssl.SSLError):
        status.update(config_updated_at=locals().get('stamp', status.get('config_updated_at', '')), sync_status='failed', error_code='config_sync_failed', error_message='Hive configuration could not be applied')
        raise


def public_status(status):
    return {key: status[key] for key in ('sync_status', 'fetch_status', 'config_updated_at', 'last_attempt_at', 'last_success_at',
            'snapshot_version', 'using_cache', 'error_code', 'error_message', 'certificate_expires_at') if key in status}


def main():
    action = sys.argv[1]
    if action == 'status-base64':
        status = load(ROOT / 'hive_status.json', {})
        # report_rc only checks the local refresh schedule; network I/O runs in a detached worker.
        next_refresh = status.get('next_refresh_epoch', status.get('attempt_epoch', 0) + INTERVAL)
        if load(CONFIG, {}).get('url') and time.time() >= next_refresh:
            subprocess.Popen([sys.executable, str(Path(__file__).resolve()), 'refresh'],
                             stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                             stderr=subprocess.DEVNULL, close_fds=True, start_new_session=True)
        if status:
            # Compressed public metadata fits the existing report_rc line buffer.
            import zlib
            value = zlib.compress(json.dumps(public_status(status), separators=(',', ':')).encode())
            print(base64.urlsafe_b64encode(value).decode().rstrip('='))
        return
    if action == 'prepare':
        # Network refresh runs asynchronously. Scans only read atomic local files.
        work = Path(sys.argv[2])
        status = load(ROOT / 'hive_status.json', {})
        config = load(CONFIG, {})
        snapshot = cached() if config.get('url') else None
        if not config.get('url'):
            status.update(fetch_status='not_configured', using_cache=False, error_code='', error_message='')
        elif snapshot is None:
            status.update(using_cache=False, snapshot_version=None)
            if not status.get('error_message'):
                status.update(fetch_status='failed', error_code='snapshot_unavailable', error_message='No available Hive snapshot')
        else:
            status['snapshot_version'] = snapshot['version']
            status['using_cache'] = status.get('fetch_status') != 'ok'
            save(work / 'hive_snapshot.json', snapshot)
        save(work / 'hive.json', public_status(status))
        return
    ROOT.mkdir(parents=True, exist_ok=True)
    RUN.mkdir(parents=True, exist_ok=True)
    with (RUN / 'hive.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | (fcntl.LOCK_NB if action == 'refresh' else 0))
        except BlockingIOError:
            return
        status = load(ROOT / 'hive_status.json', {})
        config = load(CONFIG, {})
        if action == 'sync':
            for attempt in range(4):
                try:
                    config, status, applied = sync_config(sys.argv[2], status)
                    break
                except (ValueError, KeyError, TypeError, OSError, ssl.SSLError):
                    save(ROOT / 'hive_status.json', status)
                    if attempt == 3:
                        return
                    time.sleep(2 ** attempt)
            status = refresh(config, status, force=applied)
        elif action == 'refresh':
            status = refresh(config, status)
        else:
            raise ValueError('invalid Hive operation')
        save(ROOT / 'hive_status.json', status)

if __name__ == '__main__':
    try:
        main()
    except Exception:
        # Never include config, HTTP response bodies or tokens in async_exec logs.
        print('Hive operation failed; see node Hive status', file=sys.stderr)
        sys.exit(1)
