#!/usr/bin/env python3
"""Apply/remove vCPU isolation pins, preserving other current VM settings."""
import io
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET

PIN_TAGS={'vcpupin'}

def virsh(*args):
    return subprocess.run(['virsh',*args],check=True,capture_output=True,text=True,timeout=30,env=dict(os.environ,LC_ALL='C')).stdout

def xml(domain,config=False):
    text=virsh('dumpxml',domain,*(['--inactive'] if config else []))
    for _,pair in ET.iterparse(io.StringIO(text),events=['start-ns']):
        if not re.fullmatch(r'ns\d+',pair[0]): ET.register_namespace(*pair)
    return ET.fromstring(text)

def cpus(text):
    found=set()
    for token in text.split(','):
        if not re.fullmatch(r'\d+(?:-\d+)?',token): raise ValueError('invalid CPU list')
        values=[int(v) for v in token.split('-')]
        found.update(range(values[0],values[-1]+1))
    if not found: raise ValueError('empty CPU list')
    return found

def table(command,domain,scope):
    text=virsh(command,domain,'--'+scope)
    rows={}
    for line in text.splitlines():
        m=re.fullmatch(r'\s*(\d+|\*)\s*:?\s+([\d,-]+)\s*',line)
        if m: rows[m[1]]=m[2]
    return rows

def scope_pins(domain,scope):
    rows={'vcpupin':table('vcpupin',domain,scope)}
    if not rows['vcpupin']: raise ValueError('VM vCPU affinity could not be read')
    return rows

def apply_scope(domain,scope,values,target):
    for name,rows in values.items():
        for ident in rows:
            virsh(name,domain,ident,target,'--'+scope)

def compare(expected,actual):
    for name,rows in expected.items():
        if set(rows)!=set(actual[name]): raise ValueError('VM thread layout changed')
        for ident,value in rows.items():
            if cpus(value)!=cpus(actual[name][ident]): raise ValueError('CPU affinity verification failed')

def verify(domain, scope, before, target):
    expected = {name: {ident: target for ident in rows} for name, rows in before.items()}
    compare(expected, scope_pins(domain, scope))

def allowed_cpus(domain):
    allowed = cpus(Path('/sys/devices/system/cpu/online').read_text().strip())
    # Keep the current base configuration, excluding our vCPU isolation pins.
    vcpu = xml(domain, True).find('vcpu')
    if vcpu is not None and vcpu.get('cpuset'):
        allowed &= cpus(vcpu.get('cpuset'))
    pid_files = [Path(directory) / (domain + '.pid')
                 for directory in ('/run/libvirt/qemu', '/var/run/libvirt/qemu')]
    pid_file = next((path for path in pid_files if path.is_file()), None)
    if pid_file is None:
        raise ValueError('QEMU cgroup could not be confirmed')
    pid = int(pid_file.read_text().strip())
    if b'qemu' not in Path('/proc/%d/cmdline' % pid).read_bytes():
        raise ValueError('invalid QEMU PID file')
    for entry in Path('/proc/%d/cgroup' % pid).read_text().splitlines():
        _, controllers, relative = entry.split(':', 2)
        if controllers == '':
            base, filename = Path('/sys/fs/cgroup'), 'cpuset.cpus.effective'
        elif 'cpuset' in controllers.split(','):
            base, filename = Path('/sys/fs/cgroup/cpuset'), 'cpuset.effective_cpus'
        else:
            continue
        group = base / relative.lstrip('/')
        # Use the VM parent limit, not the isolated emulator/vcpu/iothread child group.
        while group != base and re.fullmatch(r'libvirt|emulator|vcpu\d+|iothread\d+', group.name):
            group = group.parent
        allowed &= cpus((group / filename).read_text().strip())
    if not allowed:
        raise ValueError('no CPU available for normal VM scheduling')
    return ','.join(map(str, sorted(allowed)))

def remove_config_pins(domain):
    # Read current XML under the existing domain lock to preserve CPU quota changes.
    root = xml(domain, True)
    tune = root.find('cputune')
    if tune is None:
        return
    for child in list(tune):
        if child.tag in PIN_TAGS:
            tune.remove(child)
    if not list(tune) and not tune.attrib:
        root.remove(tune)
    with tempfile.NamedTemporaryFile(mode='w', suffix='.xml') as handle:
        handle.write(ET.tostring(root, encoding='unicode'))
        handle.flush()
        virsh('define', handle.name)

def main():
    mode, domain = sys.argv[1:3]
    if mode == 'release' and len(sys.argv) == 3:
        target = allowed_cpus(domain)
        before = scope_pins(domain, 'live')
        apply_scope(domain, 'live', before, target)
        remove_config_pins(domain)
        verify(domain, 'live', before, target)
        if any(e.tag in PIN_TAGS for e in xml(domain, True).findall('./cputune/*')):
            raise ValueError('persistent isolation pins remain')
    elif mode in ('apply', 'verify') and len(sys.argv) == 4:
        target = sys.argv[3]
        cpus(target)
        for scope in ('live', 'config'):
            before = scope_pins(domain, scope)
            if mode == 'apply':
                apply_scope(domain, scope, before, target)
            verify(domain, scope, before, target)
    else:
        raise ValueError('invalid affinity operation')

if __name__=='__main__':
    try: main()
    except (ValueError,KeyError,OSError,subprocess.SubprocessError) as error:
        print(str(error),file=sys.stderr);sys.exit(1)
