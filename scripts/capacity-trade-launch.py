"""Fresh, selected-field Docker preflight for a capacity-trade invocation.

The Go helper normally inspects Docker itself. Some Windows sandbox process
tokens can reach API/MySQL/Redis but cannot open Docker's named pipe from its
child process. Only for that exact denied-pipe case, the helper accepts this
operator-run launcher manifest for at most 15 seconds. It still validates its
hard-coded DB/Redis/API guards and API revision; it never accepts other projects.
"""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time

PROJECT = "project-full-stack-cap-trade"
SERVICES = (("mysql", "3306/tcp", "3450"), ("redis", "6379/tcp", "6550"))


def preflight():
    docker = shutil.which("docker")
    if not docker:
        raise RuntimeError("Docker executable unavailable")
    started = int(time.time() * 1000)
    containers = []
    for service, target, port in SERVICES:
        name = f"{PROJECT}-{service}-1"
        # Capture the full JSON privately in memory; serialize selected fields
        # only. Never print Config.Env, the full inspect result, or raw stderr.
        result = subprocess.run([docker, "inspect", name], capture_output=True,
                                text=True, encoding="utf-8", timeout=10,
                                creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        if result.returncode:
            raise RuntimeError(f"owned {service} inspection failed (exit {result.returncode})")
        objects = json.loads(result.stdout)
        if len(objects) != 1:
            raise RuntimeError("inspection did not return exactly one container")
        raw = objects[0]
        labels = raw.get("Config", {}).get("Labels", {})
        selected = {key: labels.get(key, "") for key in
                    ("com.docker.compose.project", "com.docker.compose.service")}
        bindings = raw.get("NetworkSettings", {}).get("Ports", {}).get(target) or []
        matching = [{"target": target, "host_ip": binding.get("HostIp", ""),
                     "host_port": binding.get("HostPort", "")} for binding in bindings]
        if (selected["com.docker.compose.project"] != PROJECT or
                selected["com.docker.compose.service"] != service or
                raw.get("Name") != "/" + name or not raw.get("State", {}).get("Running") or
                not re.fullmatch(r"[0-9a-f]{64}", raw.get("Id", "")) or
                not any(b["host_ip"] == "127.0.0.1" and b["host_port"] == port for b in matching)):
            raise RuntimeError(f"owned {service} labels/running/name/ID/loopback binding mismatch")
        containers.append({"id": raw["Id"], "name": raw["Name"], "service": service,
                           "running": True, "labels": selected, "ports": matching})
    if int(time.time() * 1000) - started > 12000:
        raise RuntimeError("Docker preflight took too long for the 15-second guard")
    return {"version": 1, "checked_at_unix_ms": started,
            "project": PROJECT, "containers": containers}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--helper", required=True, help="explicit local capacity-trade executable")
    parser.add_argument("command", choices=("prepare", "run", "logout"))
    args = parser.parse_args()
    helper = Path(args.helper).resolve(strict=True)
    manifest = preflight()
    descriptor, raw_path = tempfile.mkstemp(prefix="capacity-trade-preflight-", suffix=".json")
    path = Path(raw_path)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as stream:
            json.dump(manifest, stream, separators=(",", ":"))
        env = os.environ.copy()
        env["CAP_PREFLIGHT_FILE"] = str(path)
        # Explicit pipes are required with CREATE_NO_WINDOW on Windows. Merely
        # inheriting console handles can silently discard a Go child's error.
        # The helper writes only bounded completion/error lines; high-volume
        # k6 metrics and queue observations go straight to its evidence files.
        result = subprocess.run([str(helper), args.command], env=env, capture_output=True,
                                text=True, encoding="utf-8", errors="replace",
                                creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        if result.stdout:
            sys.stdout.write(result.stdout)
            sys.stdout.flush()
        if result.stderr:
            sys.stderr.write(result.stderr)
            sys.stderr.flush()
        return result.returncode
    finally:
        # This is exactly the single new file just created above, never a
        # computed recursive deletion and never the secret user-pool file.
        path.unlink(missing_ok=True)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        # No raw Docker/API/SQL diagnostic or environment values.
        print(f"capacity-trade-launch: {type(error).__name__}: {error}", file=sys.stderr)
        raise SystemExit(1)
