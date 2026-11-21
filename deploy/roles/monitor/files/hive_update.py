#!/usr/bin/env python3
"""Monitor-side Hive updater; shares the existing matcher schema validation."""
import datetime as dt
import email.utils
import fcntl
import json
from pathlib import Path
import random
import re
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parent / 'common'))
from hive_client import atomic_write, load, save, validate_snapshot

CONFIG = Path('/etc/prometheus/vm_inspection/config.json')
ROOT = CONFIG.parent
RUN = ROOT
METRICS = Path('/var/lib/node_exporter/cloudland_hive.prom')
INTERVAL = 300  # Refresh about every five minutes.
JITTER = 60  # Avoid synchronized refreshes across monitoring servers.


def utc():
    return dt.datetime.now(dt.timezone.utc).isoformat().replace('+00:00', 'Z')


def cached():
    try:
        return validate_snapshot(load(ROOT / 'hive_snapshot.json'))
    except (ValueError, TypeError, re.error):
        return None


def clear_snapshot(status):
    (ROOT / 'hive_snapshot.json').unlink(missing_ok=True)
    for key in ('etag', 'etag_url', 'snapshot_version', 'last_success_at', 'certificate_expires_at',
                'last_attempt_at', 'attempt_epoch', 'next_refresh_epoch'):
        status.pop(key, None)
    status['using_cache'] = False


class NoRedirect(urllib.request.HTTPRedirectHandler):
    # Never forward the read token to a redirect destination.
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def refresh(config, status, force=False):
    snapshot = cached()
    status.update(config_updated_at=config.get('updated_at', ''),
                  snapshot_version=snapshot['version'] if snapshot else None)
    if snapshot is None:
        status['using_cache'] = False
    if not config.get('url'):
        clear_snapshot(status)
        status.update(fetch_status='not_configured', using_cache=False, error_code='', error_message='')
        return status
    next_refresh = status.get('next_refresh_epoch', status.get('attempt_epoch', 0) + INTERVAL)
    if not force and time.time() < next_refresh:
        return status
    status['next_refresh_epoch'] = int(time.time()) + INTERVAL + random.randint(0, JITTER)
    for attempt in range(4):
        status.update(fetch_status='pending', last_attempt_at=utc(), attempt_epoch=int(time.time()))
        save(ROOT / 'hive_status.json', status)
        wait = min(300, 5 * (2 ** attempt))
        retry = True
        try:
            context = ssl.create_default_context()
            headers = {'Authorization': 'Bearer ' + config.get('token', '')}
            conditional = snapshot is not None and status.get('etag_url') == config['url'] and bool(status.get('etag'))
            if conditional:
                headers['If-None-Match'] = status['etag']
            request = urllib.request.Request(config['url'], headers=headers)
            opener = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPSHandler(context=context))
            try:
                response = opener.open(request, timeout=10)
            except urllib.error.HTTPError as error:
                if error.code != 304 or not conditional:
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
                elif code != 304 or not conditional:
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


def main():
    ROOT.mkdir(parents=True, exist_ok=True)
    with (RUN / 'hive.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        config = load(CONFIG, {})
        status = load(ROOT / 'hive_status.json', {})
        # A source change invalidates the cache before any new request. Tokens are never metrics.
        if config.get('url', '') != status.get('source_url', ''):
            clear_snapshot(status)
        changed = config.get('updated_at') != status.get('config_updated_at')
        status.update(sync_status='ok', source_url=config.get('url', ''))
        status = refresh(config, status, force=changed)
        # Do not publish a result fetched while its configuration was being replaced/deleted.
        if load(CONFIG, {}) != config:
            clear_snapshot(status)
            status.update(fetch_status='pending', sync_status='pending')
        status['available'] = bool(config.get('url') and cached() is not None and load(CONFIG, {}) == config)
        save(ROOT / 'hive_status.json', status, 0o644)
        lines = []
        for name, value in (('configured', bool(config.get('url'))), ('available', status['available']),
                            ('fetch_success', status.get('fetch_status') == 'ok'), ('updater_last_run_seconds', time.time())):
            lines.append('hive_' + name + ' ' + str(float(value)))
        if config.get('url'):
            source = urllib.parse.urlsplit(config['url'])
            # URL identity only: never expose credentials, query parameters or the Bearer token.
            url = urllib.parse.urlunsplit((source.scheme, source.netloc.rsplit('@', 1)[-1], source.path, '', ''))
            lines.append('hive_source_info{url=' + json.dumps(url, ensure_ascii=False) + '} 1')
        save_text = '\n'.join(lines) + '\n'
        atomic_write(METRICS, save_text, 0o644)


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('Hive updater failed', file=sys.stderr)
        sys.exit(1)
