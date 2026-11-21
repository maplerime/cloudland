#!/usr/bin/env python3
"""VM scan lifecycle, RPC restoration and textfile metrics."""
import base64
import datetime as dt
import fcntl
import json
import os
from pathlib import Path
import re
import socket
import sys
import time
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parent / 'common'))
from configuration import rpc_prepare, rpc_restore
from hive_client import atomic_write

METRICS_DIR = Path('/var/lib/node_exporter')
RETENTION_SECONDS = 300


def clean(value, limit=160):
    if value is None or value == '':
        return 'NA'
    return re.sub(r'[\s\x00-\x1f\x7f]+', ' ', str(value)).strip()[:limit] or 'NA'


def validate_scan(vm, scan):
    uuid.UUID(vm)
    if not re.fullmatch(re.escape(vm) + r'_\d{8}T\d{6}\.\d{9}Z', scan):
        raise ValueError('invalid scan identifier')


def notification_summary(record, action, library_configured):
    inspection = record.get('inspection_result') or {}
    state = inspection.get('collection_status')
    lines = ['采集结果：' + {'complete': '完成', 'partial': '部分成功', 'failed': '失败'}.get(state, 'NA')]
    # A stored case may retain an earlier confirmation. Notify this scan's verdict.
    evaluation = action.get('evaluation') or {}
    verdict = evaluation.get('verdict', 'not_evaluated')
    if not library_configured:
        lines.append('特征比对：未配置特征库，未判断是否存在木马或病毒')
    elif verdict in ('confirmed', 'suspected'):
        lines.extend(['特征比对：' + ('strong 命中' if verdict == 'confirmed' else 'weak 命中（可疑）'),
                      '特征库版本：' + clean(evaluation.get('ruleset_version'), 64),
                      '命中说明：' + clean(evaluation.get('match_summary') or ', '.join(evaluation.get('match_rule_ids', [])[:3]), 120)])
    elif verdict == 'no_match':
        lines.append('特征比对：已完成，未命中特征')
    else:
        lines.append('特征比对：地址已配置，本轮未完成识别')

    hive = inspection.get('hive') or {}
    if library_configured:
        lines.append('Hive：' + ('获取正常' if hive.get('fetch_status') == 'ok' else '获取异常') +
                     '；特征库版本：' + clean(hive.get('snapshot_version'), 32))
        if hive.get('error_message'):
            lines.append('Hive 原因：' + clean(hive['error_message'], 120))
    matched = [p for p in inspection.get('cputop_processes', {}).get('items', []) if p.get('matches')]
    for process in matched[:10]:
        hits = process['matches']
        text = ', '.join('%s#%s %s' % (h['category'], h['id'], h['confidence']) for h in hits[:4])
        lines.append('命中进程：%s | PID %s | %s%s' %
                     (clean(process.get('name'), 32), process.get('pid'), text, ' …' if len(hits) > 4 else ''))
        lines.append('命中依据：' + clean(json.dumps(hits[0]['conditions'], ensure_ascii=False, separators=(',', ':')), 160))

    status = record.get('isolation_status', '')
    cpus = clean(record.get('isolation_cpus'), 64)
    if status == 'isolated':
        text = '成功，已校验' if action.get('action') == 'quarantine' else '当前已隔离（此前处置）'
        lines.append('CPU 隔离：' + text + '；CPU：' + cpus)
    elif status == 'not_requested':
        if evaluation.get('pin_requested') and action.get('isolation_enabled') is False:
            lines.append('CPU 隔离：当前未隔离，本轮未执行（自动隔离开关关闭）')
        elif evaluation.get('pin_requested') is False and verdict in ('confirmed', 'suspected'):
            lines.append('CPU 隔离：当前未隔离，本轮未执行（命中策略不隔离）')
        else:
            lines.append('CPU 隔离：当前未隔离，本轮未执行')
    elif not status and record.get('error_message'):
        lines.append('CPU 隔离：当前绑核状态查询失败，本轮未执行')
        lines.append('查询原因：' + clean(record['error_message'], 160))
    elif status == 'disabled':
        lines.append('CPU 隔离：本轮未执行（自动隔离开关关闭）' if action.get('action') == 'quarantine' else 'CPU 隔离：本轮未执行（没有确诊结论）')
    elif status in ('failed', 'release_failed'):
        text = '隔离失败' if status == 'failed' else '释放失败'
        if action.get('action') != 'quarantine':
            text = '此前' + text + '；本轮未执行'
        lines.append('CPU 隔离：' + text)
        lines.append('失败原因：' + clean(record.get('error_message'), 160))
    elif status == 'released':
        lines.append('CPU 隔离：当前已释放，本轮未执行')
    else:
        lines.append('CPU 隔离：本轮未执行（没有确诊结论）')
    if status in ('isolated', 'failed', 'release_failed', 'released'):
        stamp = record.get('operation_at')
        if stamp and action.get('action') != 'record':
            label = '处置时间：' if action.get('action') == 'quarantine' else '此前处置时间：'
            lines.append(label + clean(stamp, 64) + '（UTC）')

    top = inspection.get('cputop_processes') or {}
    count = min(10, max(0, int(top.get('top_n', 10))))
    processes = (top.get('items') or [])[:count]
    if processes:
        lines.extend(['', '高 CPU 进程（前 %d 项；CPU 为 ps 口径）' % len(processes)])
        for process in processes:
            cpu = process.get('cpu_percent')
            cpu = ('%.1f%%' % cpu) if isinstance(cpu, (int, float)) else 'NA'
            lines.append('%s | PID %s | %s | %s' %
                         (clean(process.get('name'), 32), clean(process.get('pid'), 12),
                          clean(process.get('user'), 16), cpu))
    else:
        lines.extend(['', '高 CPU 进程：NA', '采集原因：' + clean(top.get('error_message'), 120)])
    return '\n\n'.join(lines)


def metric_path(vm):
    return METRICS_DIR / ('vm_inspection_' + vm + '.prom')


def marker_path(run, vm):
    return run / (vm + '.metric.collected')


def metric_header(vm):
    with metric_path(vm).open(encoding='utf-8') as source:
        fields = source.readline().split()
    if len(fields) != 4 or fields[:2] != ['#', 'vm_inspection']:
        raise ValueError('invalid notification header')
    validate_scan(vm, fields[2])
    return fields[2], int(fields[3])


def export(run, record_file, action_file, library_configured):
    record = json.loads(Path(record_file).read_text())
    action = json.loads(Path(action_file).read_text())
    vm, scan = record['instance_uuid'], record['scan_id']
    validate_scan(vm, scan)
    if action['vm_uuid'] != vm or action['scan_id'] != scan:
        raise ValueError('notification target/scan mismatch')
    labels = {key: record.get(key, '') for key in ('domain', 'region_id')}
    labels.update(instance_id=vm, source_hyper=record['source_hyper'], scan_id=scan,
                  node_hostname=socket.gethostname().split('.')[0])
    def quote(value):
        return '"' + str(value).replace('\\', '\\\\').replace('"', '\\"').replace('\n', '\\n') + '"'
    def metric(name, value, extra=None):
        values = dict(labels, **(extra or {}))
        return name + '{' + ','.join(k + '=' + quote(v) for k, v in values.items()) + '} ' + str(value) + '\n'
    completed = int(time.time())
    output = '# vm_inspection %s %d\n' % (scan, completed)
    output += metric('vm_inspection_completed_timestamp_seconds', completed)
    output += metric('vm_inspection_info', 1, {'summary': notification_summary(record, action, library_configured)})
    # The caller holds this VM's work lock, shared with collection and cleanup.
    atomic_write(metric_path(vm), output, 0o644)
    marker_path(run, vm).unlink(missing_ok=True)


def collected(run, vm, scan):
    validate_scan(vm, scan)
    try:
        current, _ = metric_header(vm)
    except FileNotFoundError:
        return
    if current == scan:
        atomic_write(marker_path(run, vm), scan + '\n', 0o600)


def cleanup(run):
    now = int(time.time())
    for marker in run.glob('*.metric.collected'):
        vm = marker.name[:-len('.metric.collected')]
        uuid.UUID(vm)
        with (run / (vm + '.work.lock')).open('a') as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                continue
            try:
                expected = marker.read_text().strip()
                current, published = metric_header(vm)
            except FileNotFoundError:
                marker.unlink(missing_ok=True)
                continue
            # An old completed report never deletes a newer scan's metric.
            if current != expected:
                marker.unlink(missing_ok=True)
            elif now - published >= RETENTION_SECONDS:
                metric_path(vm).unlink(missing_ok=True)
                marker.unlink(missing_ok=True)


ROOT = Path(os.environ.get('cache_dir', '/opt/cloudland/cache')) / 'vm_inspection' / 'scans'
METRICS = Path('/var/lib/node_exporter')


def load(path):
    return json.loads(path.read_text())


def save(path, value):
    atomic_write(path, json.dumps(value, separators=(',', ':')), 0o600)


def labels(state, extra=None):
    values = {k: str(state[k]) for k in ('instance_uuid', 'domain', 'hyper_id', 'scan_id')}
    values.update(extra or {})
    return '{' + ','.join(k + '=' + json.dumps(str(v), ensure_ascii=True) for k, v in values.items()) + '}'


def scan_metric_path(state):
    return METRICS / ('instance_scan_' + state['instance_uuid'] + '.prom')


def publish_error(state, code):
    atomic_write(METRICS / ('instance_scan_error_' + state['instance_uuid'] + '.prom'),
                 'instance_scan_failure' + labels(state, {'error_code': code}) + ' 1\n', 0o644)


def finish(state, path):
    scan_metric_path(state).unlink(missing_ok=True)
    state['phase'] = 'finished'
    save(path, state)
    try:
        rpc_restore(state)
    except Exception:
        publish_error(state, 'rpc_restore_failed')
        raise
    state['rpc_restored'] = True
    save(path, state)


def publish(state, work):
    report = load(work / 'inspection.json')
    if len(json.dumps(report).encode()) > 262144:
        raise ValueError('scan_report_limit_exceeded')
    processes = report['cputop_processes']['items']
    if report['agent']['status'] != 'ok' or report['cputop_processes']['status'] != 'ok' or not processes:
        raise ValueError('process_collection_failed')
    if len(processes) > 2048:
        raise ValueError('process_limit_exceeded')
    report['scan_id'] = state['scan_id']
    debug = ROOT / (state['instance_uuid'] + '.observations.json')
    if os.environ.get('VM_INSPECTION_KEEP_RESULTS') == 'true':
        save(debug, report)
    else:
        debug.unlink(missing_ok=True)
    context = dict(report)
    context['cputop_processes'] = dict(report['cputop_processes'], items=[])
    # Keep UI-required supplementary checks, without raw command output or unbounded labels.
    data = base64.b64encode(json.dumps(context, separators=(',', ':')).encode()).decode()
    if len(data) > 32768:
        raise ValueError('context_limit_exceeded')
    lines = ['instance_scan_flag' + labels(state) + ' 1',
             'instance_scan_collected_timestamp_seconds' + labels(state) + ' ' +
             str(dt.datetime.fromisoformat(report['finished_at'].replace('Z', '+00:00')).timestamp()),
             'instance_scan_context_info' + labels(state, {'data': data}) + ' 1']
    for process in processes:
        data = base64.b64encode(json.dumps(process, separators=(',', ':')).encode()).decode()
        if len(data) > 16384:
            raise ValueError('process_evidence_limit_exceeded')
        lines.append('instance_scan_process_info' + labels(state, {'pid': process['pid'], 'data': data}) + ' 1')
    lines.append('instance_scan_process_count' + labels(state) + ' ' + str(len(processes)))
    if sum(map(len, lines)) > 1024 * 1024:
        raise ValueError('scan_evidence_limit_exceeded')
    atomic_write(scan_metric_path(state), '\n'.join(lines) + '\n', 0o644)


def main():
    action, vm, domain = sys.argv[1:4]
    import uuid
    uuid.UUID(vm)
    if not re.fullmatch(r'[A-Za-z0-9_.-]{1,128}', domain):
        raise ValueError('invalid domain')
    ROOT.mkdir(parents=True, exist_ok=True)
    path = ROOT / (vm + '.json')
    state = load(path) if path.exists() else {}
    if action == 'begin':
        request_id, hyper, scan = sys.argv[4:7]
        validate_scan(vm, scan)
        request_epoch = int(request_id.split('_', 1)[0]) if request_id.split('_', 1)[0].isdigit() else 0
        # Same request or an unfinished round is never sampled again.
        if state and (state.get('request_id') == request_id or state.get('request_epoch', -1) >= request_epoch or state.get('phase') != 'finished' or not state.get('rpc_restored', False)):
            raise SystemExit(3)
        state = dict(instance_uuid=vm, domain=domain, hyper_id=int(hyper), scan_id=scan,
                     request_id=request_id, request_epoch=request_epoch, phase='collecting')
        save(path, state)
        atomic_write(scan_metric_path(state), 'instance_scan_flag' + labels(state) + ' 1\n' +
                     'instance_scan_started_timestamp_seconds' + labels(state) + ' ' + str(time.time()) + '\n', 0o644)
        (METRICS / ('instance_scan_error_' + vm + '.prom')).unlink(missing_ok=True)
        rpc_prepare(state, path)
    elif action == 'finish':
        if state and (not sys.argv[4] or state['scan_id'] == sys.argv[4]):
            if not state.get('rpc_restored'):
                finish(state, path)
            scan_metric_path(state).unlink(missing_ok=True)
    elif action == 'publish':
        if state.get('phase') != 'collecting':
            raise ValueError('inactive_scan')
        publish(state, Path(sys.argv[4]))
        state['phase'] = 'ready'
        save(path, state)
    elif action == 'fail':
        if state:
            publish_error(state, sys.argv[4])
            finish(state, path)
    elif action == 'check':
        if not state or state['scan_id'] != sys.argv[4] or state['phase'] not in ('ready', 'pinning'):
            raise ValueError('inactive_scan')
        if state['phase'] == 'pinning':
            if state.get('receipt'):
                print(json.dumps(state['receipt'], separators=(',', ':')))
            raise SystemExit(3)
        state['phase'] = 'pinning'
        save(path, state)
    elif action == 'save-receipt':
        receipt = load(Path(sys.argv[4]))
        if receipt['scan_id'] != state['scan_id'] or receipt['instance_uuid'] != vm:
            raise ValueError('receipt_round_mismatch')
        state['receipt'] = receipt
        save(path, state)
    else:
        raise ValueError('invalid operation')


if __name__ == '__main__':
    try:
        command, run = sys.argv[1], Path(sys.argv[2])
        if command == 'export' and len(sys.argv) == 6 and sys.argv[5] in ('true', 'false'):
            export(run, sys.argv[3], sys.argv[4], sys.argv[5] == 'true')
        elif command == 'collected' and len(sys.argv) == 5:
            collected(run, sys.argv[3], sys.argv[4])
        elif command == 'cleanup' and len(sys.argv) == 3:
            cleanup(run)
        else:
            main()
    except Exception as error:
        print('VM inspection metrics: ' + str(error), file=sys.stderr)
        sys.exit(1)
