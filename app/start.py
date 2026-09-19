#!/usr/bin/env python3
"""
AceStream Orchestrator startup script (unified Go binary).

Process tree:
  Redis             → in-process key/value store
  acestream-unified → proxy (:8000) + orchestrator (:8083) + controlplane (embedded)
  proton-sidecar    → optional Proton VPN server updater (:9099)
"""
import os
import sys
import time
import signal
import subprocess

_procs = []

def _stop_all(signum=None, frame=None, exit_code=0):
    for p in _procs:
        try:
            p.terminate()
        except Exception:
            pass
    for p in _procs:
        try:
            p.wait(timeout=40)
        except subprocess.TimeoutExpired:
            p.kill()
            p.wait()
    sys.exit(exit_code)

def start_redis():
    proc = subprocess.Popen([
        '/usr/bin/redis-server',
        '--daemonize', 'no',
        '--bind', '0.0.0.0',
        '--port', '6379',
        '--save', '',
        '--appendonly', 'no',
        '--dir', '/tmp',
        '--protected-mode', 'no',
    ])
    _procs.append(proc)
    print("Redis started", flush=True)
    for _ in range(50):
        try:
            r = subprocess.run(['/usr/bin/redis-cli', 'ping'], capture_output=True, timeout=1)
            if r.returncode == 0:
                print("Redis ready", flush=True)
                return proc
        except Exception:
            pass
        time.sleep(0.1)
    print("Redis failed to start", flush=True)
    _stop_all(exit_code=1)

def start_go_acestream():
    env = os.environ.copy()
    env.setdefault('PROXY_LISTEN_ADDR', ':8000')
    env.setdefault('ORCHESTRATOR_LISTEN_ADDR', ':8083')
    env.setdefault('REDIS_HOST', 'localhost')
    env.setdefault('REDIS_PORT', '6379')
    p = subprocess.Popen(
        ['/usr/local/bin/acestream-unified'],
        env=env, stdout=sys.stdout, stderr=sys.stderr,
    )
    _procs.append(p)
    print(f"Go unified binary started (pid={p.pid})", flush=True)
    return p

def start_proton_sidecar():
    """Start the Proton VPN server updater sidecar (optional)."""
    if os.getenv('DISABLE_PROTON_SIDECAR', '').lower() in ('1', 'true', 'yes'):
        print("Proton sidecar disabled via DISABLE_PROTON_SIDECAR", flush=True)
        return None
    env = os.environ.copy()
    env.setdefault('PROTON_STORAGE_PATH', '/app/app/config/proton')
    p = subprocess.Popen(
        [
            'python3', '-m', 'uvicorn',
            'app.proton_service:app',
            '--host', '127.0.0.1',
            '--port', '9099',
            '--no-access-log',
        ],
        cwd='/app',
        env=env, stdout=sys.stdout, stderr=sys.stderr,
    )
    _procs.append(p)
    print(f"Proton sidecar started (pid={p.pid})", flush=True)
    return p

def main():
    signal.signal(signal.SIGTERM, _stop_all)
    signal.signal(signal.SIGINT, _stop_all)
    print("Starting AceStream Orchestrator Stack (unified Go binary)...", flush=True)
    redis_proc = start_redis()

    go_proc = start_go_acestream()
    proton_proc = start_proton_sidecar()

    print("Stack initialized. Monitoring critical processes...", flush=True)

    while True:
        time.sleep(2)
        if go_proc.poll() is not None or redis_proc.poll() is not None:
            print(f"CRITICAL: stack process exited (go={go_proc.returncode}, redis={redis_proc.returncode})", flush=True)
            _stop_all(exit_code=1)
        # Proton sidecar is optional — restart it if it dies, don't kill the stack.
        if proton_proc is not None and proton_proc.poll() is not None:
            print(f"WARNING: Proton sidecar exited (code={proton_proc.returncode}), restarting...", flush=True)
            _procs.remove(proton_proc)
            proton_proc = start_proton_sidecar()

if __name__ == '__main__':
    main()
