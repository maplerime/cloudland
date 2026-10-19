#!/usr/bin/env python3
"""Enrich only selected PIDs, then apply Hive AND rules to each single object."""
import base64
import fnmatch
import ipaddress
import json
from pathlib import Path
import re
import sys

from hive_client import DEFAULT_POLICY, load, save, validate_policy, validate_snapshot

FIELDS = {'process': ('exe_path', 'cmdline_regex', 'parent_exe', 'uid', 'exe_deleted'),
          'file': ('path', 'sha256', 'size'), 'network': ('kind', 'value', 'port', 'proto')}


def merge(work):
    report = load(work / 'result.json')
    report['hive'] = load(work / 'hive.json', {'fetch_status': 'failed', 'error_message': 'Hive status unavailable'})
    report['match_policy'] = validate_policy(load(work / 'policy.json', DEFAULT_POLICY))
    evidence = {}
    for line in (work / 'process_evidence.out').read_text(errors='replace').splitlines() if (work / 'process_evidence.out').exists() else []:
        parts = line.split('|')
        if len(parts) != 5 or parts[0] != 'PROCESS' or not parts[1].isdigit():
            continue
        pid, field, state = int(parts[1]), parts[2], parts[3]
        item = evidence.setdefault(pid, {'connections': [], 'errors': {}})
        try:
            raw = base64.b64decode(parts[4], validate=True)
            value = raw.decode('utf-8', errors='replace')
            if state != 'ok':
                item['errors'][field] = value
                if field in ('connection', 'connections'):
                    item['connections_status'] = 'failed'
            elif field == 'argv':
                argv = raw.split(b'\x00')
                if argv and argv[-1] == b'':
                    argv.pop()
                item['cmdline'] = ' '.join(v.decode('utf-8', errors='replace') for v in argv)
            elif field in ('exe', 'parent_exe', 'exe_sha256') and value == 'null':
                item[field] = None
            elif field == 'parent_exe':
                item[field] = value[:-10] if value.endswith(' (deleted)') else value
            elif field == 'connection':
                proto, peer = value.split('|', 1)
                address, port = peer.rsplit(':', 1)
                address = address.strip('[]').split('%', 1)[0]
                if port.isdigit():
                    item['connections'].append({'dst': str(ipaddress.ip_address(address)), 'port': int(port), 'proto': proto})
            elif field in ('uid', 'exe_size'):
                item[field] = int(value) if value != 'null' else None
            elif field == 'exe_deleted':
                if value not in ('true', 'false'):
                    raise ValueError('invalid deleted flag')
                item[field] = value == 'true'
            elif field == 'connections':
                item['connections_status'] = 'ok'
            else:
                item[field] = value
        except (ValueError, TypeError):
            item['errors'][field] = 'invalid_observation'
            if field in ('connection', 'connections'):
                item['connections_status'] = 'failed'
    for process in report['cputop_processes']['items']:
        observation = evidence.get(process['pid'], {'errors': {'_process': 'not_collected'}, 'connections': []})
        # A failed batch must not turn a partially emitted stream into a clean result.
        if load(work / 'process_evidence.rc') != 0 and (work / 'hive_snapshot.json').exists():
            observation['errors']['_process'] = 'evidence_collection_incomplete'
        process.update(observation)
    if (work / 'hive_snapshot.json').exists() and report['collection_status'] == 'complete':
        if any(p.get('errors') for p in report['cputop_processes']['items']):
            report['collection_status'] = 'partial'
    save(work / 'result.json', report)


def compare(category, rule, observation, errors):
    """Return True/False/None: hit, known mismatch, unavailable evidence."""
    unknown = False
    keys = FIELDS[category]
    for key in keys:
        expected = rule.get(key)
        if expected is None or expected == '':
            continue
        if category == 'network' and key == 'kind':
            continue
        field = {'exe_path': 'exe', 'cmdline_regex': 'cmdline'}.get(key, key)
        if category == 'network' and key == 'value':
            field = 'domain' if rule['kind'] == 'domain' else 'dst'
        if field not in observation or field in errors or (field == 'cmdline' and 'argv' in errors):
            unknown = True
            continue
        actual = observation[field]
        if actual is None:
            return False
        if key in ('exe_path', 'parent_exe', 'path'):
            matched = fnmatch.fnmatchcase(actual, expected)
        elif key == 'cmdline_regex':
            matched = re.search(expected, actual) is not None
        elif key == 'sha256':
            matched = actual.lower() == expected
        elif category == 'network' and key == 'value':
            if rule['kind'] == 'domain':
                matched = actual.lower().rstrip('.') == expected.lower().rstrip('.')
            elif rule['kind'] == 'ip':
                matched = ipaddress.ip_address(actual) == ipaddress.ip_address(expected)
            else:
                matched = ipaddress.ip_address(actual) in ipaddress.ip_network(expected)
        else:
            matched = actual == expected
        if not matched:
            return False
    return None if unknown else True


def evaluate(report, snapshot):
    all_hits, incomplete = [], False
    processes = report.get('cputop_processes', {}).get('items', [])
    for process in processes:
        hits, unavailable = [], snapshot is None or '_process' in process.get('errors', {})
        errors = process.get('errors', {})
        if snapshot is not None and '_process' not in errors:
            file = {target: process[source] for source, target in (('exe', 'path'), ('exe_sha256', 'sha256'), ('exe_size', 'size')) if source in process}
            file_errors = {target: errors[source] for source, target in (('exe', 'path'), ('exe_sha256', 'sha256'), ('exe_size', 'size')) if source in errors}
            for category in FIELDS:
                observations = [process] if category == 'process' else [file] if category == 'file' else process.get('connections', [])
                bad = errors if category == 'process' else file_errors if category == 'file' else {}
                if category == 'network' and snapshot[category] and process.get('connections_status') != 'ok':
                    unavailable = True
                for rule in snapshot[category]:
                    for observation in observations:
                        outcome = compare(category, rule, observation, bad)
                        if outcome is None:
                            # Domain evidence is optional; its feature is skipped, not guessed.
                            if not (category == 'network' and rule['kind'] == 'domain' and not observation.get('domain')):
                                unavailable = True
                        elif outcome:
                            conditions = {k: rule[k] for k in FIELDS[category] if rule.get(k) is not None and rule.get(k) != ''}
                            mapping = {'exe_path': 'exe', 'cmdline_regex': 'cmdline'}
                            if category == 'network':
                                mapping['value'] = 'domain' if rule['kind'] == 'domain' else 'dst'
                            observed_keys = [mapping.get(k, k) for k in conditions if k != 'kind']
                            hit = {'category': category, 'id': rule['id'], 'confidence': rule['confidence'], 'family': rule.get('family', ''),
                                   'conditions': conditions, 'evidence': {k: observation[k] for k in observed_keys if k in observation}}
                            if hit not in hits:
                                hits.append(hit)
        process['matches'] = hits
        process['match_status'] = 'matched' if hits else 'not_evaluated' if unavailable else 'no_match'
        if unavailable:
            process['match_error'] = 'Some required process evidence is unavailable' if snapshot else 'No available Hive snapshot'
        all_hits.extend((process, hit) for hit in hits)
        incomplete = incomplete or unavailable
    if all_hits:
        verdict = 'confirmed' if any(h['confidence'] == 'strong' for _, h in all_hits) else 'suspected'
    elif snapshot is None or incomplete or report.get('cputop_processes', {}).get('status') != 'ok':
        verdict = 'not_evaluated'
    else:
        verdict = 'no_match'
    policy = validate_policy(report.get('match_policy', DEFAULT_POLICY))
    ids = list(dict.fromkeys('%s#%s' % (h['category'], h['id']) for _, h in all_hits))
    summary = '; '.join('PID %s %s#%s (%s)' % (p['pid'], h['category'], h['id'], h['confidence']) for p, h in all_hits)
    if len(summary) > 256:
        suffix = '; see full record'
        summary = summary[:256 - len(suffix)] + suffix
    return {'verdict': verdict, 'ruleset_version': str(snapshot['version']) if snapshot else '', 'match_rule_ids': ids,
            'match_summary': summary, 'pin_requested': any(policy[h['confidence']]['action'] == 'pin' for _, h in all_hits)}


def main():
    if sys.argv[1] == 'merge':
        merge(Path(sys.argv[2]))
    elif sys.argv[1] == 'evaluate':
        path = Path(sys.argv[2])
        report = load(path)
        snapshot_path = Path(sys.argv[3])
        snapshot = validate_snapshot(load(snapshot_path)) if snapshot_path.exists() else None
        result = evaluate(report, snapshot)
        save(path, report)
        print(json.dumps(result, separators=(',', ':')))
    else:
        raise ValueError('invalid matching operation')


if __name__ == '__main__':
    main()
