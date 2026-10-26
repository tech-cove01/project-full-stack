"""Check a prebuilt Go API against real MySQL/Redis, using only Python's stdlib.

Run with the isolated MYSQL_* and REDIS_* environment from backend-go/.env.example:
    python scripts/go-bootstrap-smoke.py --binary backend-go/api.exe --output report.json

Each scenario starts its own process and uses an ephemeral loopback API port.
This script creates no fixture database rows. API workers can initialize the
order Stream/group, consume existing orders and deliver activity outbox events;
run against isolated dependencies after other fixture producers have finished.
"""

import argparse
import datetime
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.request import build_opener, ProxyHandler


STARTUP_TIMEOUT = 15.0
SHUTDOWN_TIMEOUT = 12.0
SERVICE = 'project-full-stack-go'
HEALTH_PATHS = ('/health/live', '/health/ready', '/actuator/health')
REQUIRED_ENV = (
    'MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD',
    'REDIS_HOST', 'REDIS_PORT',
)


class Redactor:
    def __init__(self, environment):
        self.passwords = sorted({
            value for key, value in environment.items()
            if key.upper().endswith('PASSWORD') and value
        }, key=len, reverse=True)

    def contains_password(self, value):
        return any(password in value for password in self.passwords)

    def text(self, value):
        for password in self.passwords:
            value = value.replace(password, '[REDACTED]')
        # Also remove unexpected credential-shaped log fields and URL credentials.
        value = re.sub(
            r'(?i)(\b(?:password|passwd|token|authorization|code)\b["\']?\s*[=:]\s*)'
            r'(?:"[^"\r\n]*"|\'[^\'\r\n]*\'|[^\s,;}]+)',
            r'\1[REDACTED]', value,
        )
        return re.sub(r'(://[^\s/:@]+:)[^\s@]+@', r'\1[REDACTED]@', value)

    def object(self, value):
        if isinstance(value, str):
            return self.text(value)
        if isinstance(value, list):
            return [self.object(item) for item in value]
        if isinstance(value, dict):
            return {key: self.object(item) for key, item in value.items()}
        return value


def reserve_port(listening=False):
    """Keep the port reserved; without listen(), TCP connections are refused."""
    reserved = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        if os.name == 'nt':
            reserved.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
        reserved.bind(('127.0.0.1', 0))
        if listening:
            reserved.listen(1)
        return reserved
    except BaseException:
        reserved.close()
        raise


def api_port():
    with reserve_port() as reserved:
        return reserved.getsockname()[1]


def can_rebind(port):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        if os.name == 'nt':
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
        else:
            # Go's Unix TCP listener also uses SO_REUSEADDR for restartability.
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            probe.bind(('127.0.0.1', port))
            probe.listen(1)
            return True
        except OSError:
            return False


def health(opener, port, path, timeout):
    url = f'http://127.0.0.1:{port}{path}'
    try:
        response = opener.open(url, timeout=timeout)
    except HTTPError as error:
        response = error
    with response:
        body = json.load(response)
        status = response.status
    matches = (
        status == 200 and isinstance(body, dict)
        and body.get('status') == 'UP'
        and body.get('service') == SERVICE
        and body.get('runtime') == 'go'
    )
    return {'path': path, 'http_status': status, 'body': body, 'passed': matches}


def await_health(process, port, opener, deadline):
    last_error = 'API did not become healthy'
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError('API exited before readiness')
        checks = []
        try:
            for path in HEALTH_PATHS:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError('startup deadline reached')
                checks.append(health(opener, port, path, min(1.0, remaining)))
            if all(check['passed'] for check in checks):
                return checks
            last_error = 'health response did not identify a healthy Go API'
        except (OSError, URLError, ValueError, TimeoutError) as error:
            last_error = f'{type(error).__name__}: {error}'
        time.sleep(min(0.1, max(0.0, deadline - time.monotonic())))
    raise TimeoutError(last_error)


def graceful_shutdown(process):
    if os.name == 'nt':
        requested_signal = 'CTRL_BREAK_EVENT'
        process.send_signal(signal.CTRL_BREAK_EVENT)
    else:
        requested_signal = 'SIGTERM'
        process.send_signal(signal.SIGTERM)
    return requested_signal, process.wait(timeout=SHUTDOWN_TIMEOUT)


def run_scenario(binary, base_environment, name, opener, redactor):
    started = time.monotonic()
    result = {
        'scenario': name, 'passed': False, 'health': [], 'exit_code': None,
        'forced_cleanup': False, 'password_in_raw_log': False,
    }
    environment = base_environment.copy()
    reserved = None
    process = None
    port = api_port()
    if name == 'missing_mysql_password':
        environment.pop('MYSQL_PASSWORD', None)
    elif name == 'missing_mysql_port':
        environment.pop('MYSQL_PORT', None)
    elif name in ('mysql_connection_refused', 'redis_connection_refused'):
        reserved = reserve_port()
        dependency = 'MYSQL' if name.startswith('mysql') else 'REDIS'
        environment[f'{dependency}_HOST'] = '127.0.0.1'
        environment[f'{dependency}_PORT'] = str(reserved.getsockname()[1])
    elif name == 'api_port_in_use':
        reserved = reserve_port(listening=True)
        port = reserved.getsockname()[1]
    environment['API_ADDR'] = f'127.0.0.1:{port}'
    result['api_address'] = environment['API_ADDR']
    # The health/startup checks must not depend on a machine's HTTP proxy.
    creationflags = subprocess.CREATE_NEW_PROCESS_GROUP if os.name == 'nt' else 0
    with tempfile.TemporaryFile() as log:
        try:
            process = subprocess.Popen(
                [str(binary)], env=environment, stdout=log, stderr=subprocess.STDOUT,
                stdin=subprocess.DEVNULL, creationflags=creationflags,
            )
            deadline = time.monotonic() + STARTUP_TIMEOUT
            if name == 'healthy_start_and_shutdown':
                result['health'] = await_health(process, port, opener, deadline)
                result['startup_seconds'] = round(time.monotonic() - started, 3)
                if process.poll() is not None:
                    raise RuntimeError('API exited before the shutdown signal')
                result['shutdown_signal'] = 'CTRL_BREAK_EVENT' if os.name == 'nt' else 'SIGTERM'
                requested_signal, exit_code = graceful_shutdown(process)
                result['shutdown_signal'] = requested_signal
                result['exit_code'] = exit_code
                result['api_port_rebindable'] = can_rebind(port)
                if exit_code != 0:
                    raise RuntimeError('graceful shutdown did not exit with code zero')
                if not result['api_port_rebindable']:
                    raise RuntimeError('API port could not be rebound after shutdown')
            else:
                exit_code = process.wait(timeout=max(0.01, deadline - time.monotonic()))
                result['exit_code'] = exit_code
                if exit_code == 0:
                    raise RuntimeError('invalid configuration unexpectedly exited with code zero')
            result['passed'] = True
        except Exception as error:
            result['error'] = redactor.text(f'{type(error).__name__}: {error}')
        finally:
            if process is not None:
                if process.poll() is None:
                    result['forced_cleanup'] = True
                    # Cleanup is explicitly a failure, never evidence of graceful shutdown.
                    result['passed'] = False
                    process.kill()
                    process.wait(timeout=5)
                result['exit_code'] = process.returncode
            if reserved is not None:
                reserved.close()
            log.seek(0)
            raw_log = log.read().decode('utf-8', errors='replace')
            result['password_in_raw_log'] = redactor.contains_password(raw_log)
            result['log'] = redactor.text(raw_log)
            if result['password_in_raw_log']:
                result['passed'] = False
                result['error'] = 'API printed an environment password; evidence was redacted'
            if name != 'healthy_start_and_shutdown' and not raw_log.strip():
                result['passed'] = False
                result.setdefault('error', 'failure did not provide a diagnostic log')
            expected_diagnostic = {
                'missing_mysql_password': r'MYSQL_PASSWORD',
                'missing_mysql_port': r'MYSQL_PORT',
                'mysql_connection_refused': r'mysql',
                'redis_connection_refused': r'redis',
                'api_port_in_use': r'listen|bind|address',
            }.get(name)
            if expected_diagnostic and not re.search(expected_diagnostic, raw_log, re.IGNORECASE):
                result['passed'] = False
                result.setdefault('error', 'failure log did not identify the expected configuration or dependency')
            result['duration_seconds'] = round(time.monotonic() - started, 3)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path, help='prebuilt Go API executable')
    parser.add_argument('--output', type=Path, help='also save the redacted JSON report here')
    args = parser.parse_args()
    environment = dict(os.environ)
    redactor = Redactor(environment)
    binary = args.binary.expanduser().resolve()
    if not binary.is_file():
        parser.error('--binary must name an existing prebuilt executable')
    missing = [key for key in REQUIRED_ENV if not environment.get(key)]
    if missing:
        parser.error('set the isolated dependency environment first; missing ' + ', '.join(missing))
    # Bound the experiment even when the invoking shell has longer service deadlines.
    environment['GO_STARTUP_TIMEOUT'] = '8s'
    environment['GO_HEALTH_TIMEOUT'] = '2s'
    environment['GO_SHUTDOWN_TIMEOUT'] = '10s'
    opener = build_opener(ProxyHandler({}))
    scenarios = (
        'healthy_start_and_shutdown', 'missing_mysql_password', 'missing_mysql_port',
        'mysql_connection_refused', 'redis_connection_refused', 'api_port_in_use',
    )
    results = [run_scenario(binary, environment, name, opener, redactor) for name in scenarios]
    report = redactor.object({
        'task': 'GO-001', 'acceptance': 'MIG-GO-01',
        'recorded_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'binary': str(binary), 'platform': os.name, 'startup_timeout_seconds': STARTUP_TIMEOUT,
        'service_deadlines': {'startup': '8s', 'health': '2s', 'shutdown': '10s'},
        'passed': all(result['passed'] for result in results), 'scenarios': results,
    })
    encoded = json.dumps(report, ensure_ascii=False, indent=2)
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(encoded + '\n', encoding='utf-8')
    print(encoded)
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
