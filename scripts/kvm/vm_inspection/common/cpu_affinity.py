#!/usr/bin/env python3
"""Save/apply/restore only libvirt CPU affinity. Never restore an old whole-domain XML."""
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET

PIN_TAGS={'vcpupin','emulatorpin','iothreadpin'}
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

def pins(root):
    return [(e.tag,dict(e.attrib)) for e in root.findall('./cputune/*') if e.tag in PIN_TAGS]

def capture(domain):
    live=xml(domain);config=xml(domain,True)
    if live.findtext('uuid')!=config.findtext('uuid'): raise ValueError('live/config identity mismatch')
    state={'domain':domain,'domain_uuid':live.findtext('uuid'),'config_pins':pins(config),'scopes':{}}
    for scope in ('live','config'):
        current=live if scope=='live' else config
        has_iothreads=int(current.findtext('iothreads') or '0')>0
        item={'vcpupin':table('vcpupin',domain,scope),'emulatorpin':table('emulatorpin',domain,scope),'iothreadpin':table('iothreadinfo',domain,scope) if has_iothreads else {}}
        if has_iothreads and not item['iothreadpin']: raise ValueError('IOThread affinity could not be read')
        if not item['vcpupin'] or not item['emulatorpin']: raise ValueError('incomplete affinity snapshot')
        state['scopes'][scope]=item
    return state

def identity(domain,state):
    if state['domain']!=domain or xml(domain).findtext('uuid')!=state['domain_uuid']: raise ValueError('saved affinity belongs to another domain')

def apply_scope(domain,scope,values,target=None):
    for name,rows in values.items():
        for ident,original in rows.items():
            mask=target if target is not None else original
            if name=='emulatorpin': virsh('emulatorpin',domain,mask,'--'+scope)
            else: virsh(name,domain,ident,mask,'--'+scope)

def compare(expected,actual):
    for name,rows in expected.items():
        if set(rows)!=set(actual[name]): raise ValueError('VM thread layout changed')
        for ident,value in rows.items():
            if cpus(value)!=cpus(actual[name][ident]): raise ValueError('CPU affinity verification failed')

def restore_config(domain,state):
    # Merge affinity fields into freshly read config so quotas and other settings remain current.
    # The shell caller holds the same lock as adjust_cpu_hotplug.sh through define/verification.
    root=xml(domain,True)
    if root.findtext('uuid')!=state['domain_uuid']: raise ValueError('persistent identity mismatch')
    tune=root.find('cputune')
    if tune is None: tune=ET.SubElement(root,'cputune')
    for child in list(tune):
        if child.tag in PIN_TAGS: tune.remove(child)
    for tag,attrs in state['config_pins']: ET.SubElement(tune,tag,attrs)
    if not list(tune) and not tune.attrib: root.remove(tune)
    with tempfile.NamedTemporaryFile(mode='w',suffix='.xml',dir=Path(sys.argv[3]).parent) as handle:
        handle.write(ET.tostring(root,encoding='unicode'));handle.flush()
        virsh('define',handle.name)

def main():
    mode,domain,filename=sys.argv[1:4]
    if mode=='capture':
        print(json.dumps(capture(domain)));return
    state=json.loads(Path(filename).read_text());identity(domain,state)
    if mode=='apply':
        target=sys.argv[4];cpus(target)
        for scope,values in state['scopes'].items(): apply_scope(domain,scope,values,target)
    elif mode=='restore':
        apply_scope(domain,'live',state['scopes']['live'])
        restore_config(domain,state)
    elif mode not in ('verify','verify-restored'): raise ValueError('unknown affinity operation')
    if mode in ('apply','verify'):
        actual=capture(domain);target=sys.argv[4]
        for scope,values in state['scopes'].items():
            desired={name:{ident:target for ident in rows} for name,rows in values.items()}
            compare(desired,actual['scopes'][scope])
    else:
        actual=capture(domain)
        for scope,values in state['scopes'].items(): compare(values,actual['scopes'][scope])
        if sorted(state['config_pins'],key=str)!=sorted(pins(xml(domain,True)),key=str): raise ValueError('original explicit/implicit config pinning not restored')
if __name__=='__main__':
    try: main()
    except (ValueError,KeyError,OSError,subprocess.SubprocessError) as error:
        print(str(error),file=sys.stderr);sys.exit(1)
