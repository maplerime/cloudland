#!/usr/bin/env python3
"""Publish scan notifications and retire collected snapshots from report_rc."""
import fcntl
import json
import os
from pathlib import Path
import re
import socket
import sys
import tempfile
import time
import uuid

METRICS_DIR = Path('/var/lib/node_exporter')
RETENTION_SECONDS = 300


def clean(value, limit=160):
    if value is None or value == '':
        return 'NA'
    return re.sub(r'[\s\x00-\x1f\x7f]+', ' ', str(value)).strip()[:limit] or 'NA'


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
    elif verdict == 'confirmed':
        lines.extend(['特征比对：命中',
                      '特征库版本：' + clean(evaluation.get('ruleset_version'), 64),
                      '命中说明：' + clean(evaluation.get('match_summary') or ', '.join(evaluation.get('match_rule_ids', [])[:3]), 120)])
    elif verdict == 'no_match':
        lines.append('特征比对：已完成，未命中特征')
    else:
        lines.append('特征比对：地址已配置，本轮未完成识别')

    status = record.get('isolation_status', '')
    cpus = clean(record.get('isolation_cpus'), 64)
    if status == 'isolated':
        text = '成功，已校验' if action.get('action') == 'quarantine' else '当前已隔离（此前处置）'
        lines.append('CPU 隔离：' + text + '；CPU：' + cpus)
    elif status == 'not_requested':
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
            raise ValueError('invalid notification operation')
    except Exception as error:
        print('VM inspection metrics: ' + str(error), file=sys.stderr)
        sys.exit(1)
