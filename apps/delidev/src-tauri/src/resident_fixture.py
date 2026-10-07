#!/usr/bin/python3
# SPDX-License-Identifier: Apache-2.0
# Test-only adapter for deterministic closed-operation shell fixtures.
import base64, json, os, subprocess, sys, threading
operation = OPERATION_PATH
root = sys.argv[2]
write_lock = threading.Lock()
children_lock = threading.Lock()
children = []
stopping = threading.Event()
def send(value):
    with write_lock:
        print(json.dumps(value), flush=True)
send({'version': 2, 'result': {'endpoint':'http://127.0.0.1:46310', 'generation':'019c2381-9300-7000-8000-000000000001', 'key': base64.urlsafe_b64encode(bytes([9]*32)).decode().rstrip('=')}})
def execute(request):
    op = request['operation']
    args = ['--data-dir', root]
    if request.get('scope') == 'local':
        args[1] = os.path.join(root, 'desktop-client')
    elif request.get('scope','').startswith('saved:'):
        args[1] = os.path.join(root,'connections',request['scope'][6:],'client')
    if request.get('request_id'):
        args += ['--request-id',request['request_id']]
    if op.startswith('runtime.'):
        args += ['server','desktop-host','--listen','127.0.0.1:46310','--allowed-origins','tauri://localhost,http://tauri.localhost,http://127.0.0.1:46311','--mode',op[8:]]
    else:
        args += op.split('.') + request.get('arguments',[])
        if op in ('device.inspect','device.pair-local'):
            args += ['--join-existing','--device-dir',os.path.join(root,'desktop-client')]
        if op == 'server.desktop-status':
            args += ['--listen','127.0.0.1:46310','--allowed-origins','tauri://localhost,http://tauri.localhost,http://127.0.0.1:46311']
    child = subprocess.Popen([operation]+args,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL)
    with children_lock:
        children.append(child)
    try:
        output,_ = child.communicate(base64.b64decode(request.get('input','')))
        result = json.loads(output)
        result['version'] = 2
        result['id'] = request['id']
    except Exception:
        result = {'version':2,'id':request['id'],'error':{'code':'unavailable'}}
    finally:
        with children_lock:
            children.remove(child)
    if not stopping.is_set():
        send(result)
for line in sys.stdin:
    request = json.loads(line)
    if request['operation'] == 'runtime.shutdown':
        break
    if request['operation'] in ('runtime.cancel','runtime.fence'):
        continue
    threading.Thread(target=execute,args=(request,),daemon=True).start()
stopping.set()
with children_lock:
    for child in children:
        child.kill()
