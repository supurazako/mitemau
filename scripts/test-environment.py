#!/usr/bin/env python3
"""Run the local UI test stack and open synthetic accounts from a role picker."""
import hashlib
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import tempfile
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parents[1]
HOST = "mitemau-test.localhost"
APP = f"http://{HOST}:5175"
PICKER = f"http://{HOST}:5176"
PROFILES = {
    "viewer": "Viewer",
    "pending": "Pending broadcaster",
    "broadcaster": "Approved broadcaster",
    "posting-blocked": "Comment posting suspended",
    "broadcast-blocked": "Broadcasting suspended",
    "staff": "Staff",
}
ACCOUNTS = {role: secrets.token_hex(32) for role in PROFILES}
# Start subprocesses with only the environment needed by the local tools.
BASE_ENV = {key: os.environ[key] for key in ("PATH", "HOME", "LANG", "DOCKER_CONFIG") if key in os.environ}
DB_PASSWORD = secrets.token_hex(24)
COMPOSE_ENV = {
    **BASE_ENV,
    "MITEMAU_TEST_DB_PASSWORD": DB_PASSWORD,
    "MITEMAU_TEST_DB_ROOT_PASSWORD": secrets.token_hex(24),
}
COMPOSE = [
    "docker", "compose", "--env-file", os.devnull, "-f", str(ROOT / "compose.test.yaml"),
    "-p", f"mitemau-ui-test-{secrets.token_hex(4)}",
]


def compose(*args, **kwargs):
    return subprocess.run(COMPOSE + list(args), env=COMPOSE_ENV, cwd=ROOT, check=True, **kwargs)


def sql(statements):
    compose(
        "exec", "-T", "mysql", "sh", "-c",
        'MYSQL_PWD="$MYSQL_PASSWORD" mysql -u "$MYSQL_USER" "$MYSQL_DATABASE"',
        input="START TRANSACTION;\n" + ";\n".join(statements) + ";\nCOMMIT;\n",
        text=True, stdout=subprocess.DEVNULL,
    )


def seed():
    statements = []
    for role, name in PROFILES.items():
        account = f"UNHEX('{ACCOUNTS[role]}')"
        statements.extend([
            f"INSERT INTO accounts(id,email,created_at) VALUES ({account},'{role}@example.test',UTC_TIMESTAMP(6))",
            f"INSERT INTO account_profiles(account_id,display_id,display_name) VALUES ({account},'{role}','{name}')",
        ])
        if role != "viewer":
            approved = "NULL" if role == "pending" else "UTC_TIMESTAMP(6)"
            statements.append(
                "INSERT INTO broadcaster_applications(account_id,requested_at,approved_at) "
                f"VALUES ({account},UTC_TIMESTAMP(6),{approved})"
            )
        if role == "posting-blocked":
            statements.append(
                "INSERT INTO account_comment_permissions(account_id,posting_allowed,suspension_reason) "
                f"VALUES ({account},FALSE,'Please follow the comment rules.')"
            )
        if role == "broadcast-blocked":
            statements.append(
                "INSERT INTO account_broadcast_permissions(account_id,broadcasting_allowed,suspension_reason) "
                f"VALUES ({account},FALSE,'Please follow the broadcasting rules.')"
            )
    # Synthetic shared stream and report exercise viewer, permissions and staff pages.
    creator = f"UNHEX('{ACCOUNTS['broadcaster']}')"
    stream = f"UNHEX('{secrets.token_hex(16)}')"
    reporter = f"UNHEX('{ACCOUNTS['viewer']}')"
    report = f"UNHEX('{secrets.token_hex(16)}')"
    statements.extend([
        "INSERT INTO live_streams(id,creator_id,title,state,created_at,started_at) "
        f"VALUES ({stream},{creator},'Test stream','live',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))",
        "INSERT INTO stream_participants(stream_id,account_id,joined_at) "
        f"VALUES ({stream},{creator},UTC_TIMESTAMP(6))",
        "INSERT INTO user_reports(id,reporter_id,target_id,reason,incident_at,stream_id,created_at,expires_at) "
        f"VALUES ({report},{reporter},{creator},'Sample report',UTC_TIMESTAMP(6),{stream},"
        "UTC_TIMESTAMP(6),DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 180 DAY))",
    ])
    sql(statements)


def session(role):
    token = secrets.token_hex(32)
    digest = hashlib.sha256(token.encode()).hexdigest()
    sql([
        "INSERT INTO account_sessions(token_hash,account_id,expires_at,absolute_expires_at) "
        f"VALUES (UNHEX('{digest}'),UNHEX('{ACCOUNTS[role]}'),"
        "DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 HOUR),DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 HOUR))"
    ])
    return token


class Picker(BaseHTTPRequestHandler):
    def is_picker(self):
        return self.path == "/" and self.headers.get("Host") == f"{HOST}:5176"

    def do_GET(self):
        if not self.is_picker():
            self.send_error(404)
            return
        buttons = "".join(f'<p><button name="role" value="{role}">{name}</button></p>' for role, name in PROFILES.items())
        body = (
            '<!doctype html><html lang="en"><meta charset="utf-8">'
            '<meta name="viewport" content="width=device-width, initial-scale=1">'
            '<title>Mitemau test accounts</title><main><h1>Mitemau test accounts</h1>'
            f'<form method="post">{buttons}<p><button name="role" value="anonymous">Signed out</button></p>'
            '</form></main></html>'
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Security-Policy", f"default-src 'none'; form-action 'self' {APP}; frame-ancestors 'none'")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        if not self.is_picker() or self.headers.get("Origin") != PICKER:
            self.send_error(403)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 128:
                raise ValueError()
            role = parse_qs(self.rfile.read(length).decode()).get("role", [""])[0]
        except ValueError:
            self.send_error(400)
            return
        if role not in PROFILES and role != "anonymous":
            self.send_error(400)
            return
        try:
            token = "" if role == "anonymous" else session(role)
        except (OSError, subprocess.CalledProcessError):
            self.send_error(503)
            return
        self.send_response(303)
        self.send_header("Cache-Control", "no-store")
        age = 3600 if token else 0
        self.send_header("Set-Cookie", f"mitemau_session={token}; Path=/; Max-Age={age}; HttpOnly; SameSite=Strict")
        destination = "/broadcasting" if role in ("staff", "pending", "broadcaster", "broadcast-blocked") else "/"
        self.send_header("Location", APP + destination)
        self.end_headers()


def wait_for(url, process):
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError(f"Process exited while starting {url}")
        try:
            with urlopen(url, timeout=1) as response:
                if response.status == 200:
                    return
        except OSError:
            time.sleep(.25)
    raise RuntimeError(f"Timed out waiting for {url}")


def main():
    go = shutil.which("go") or "/usr/local/go/bin/go"
    vite = ROOT / "frontend/node_modules/vite/bin/vite.js"
    if not vite.is_file():
        raise SystemExit("Run npm ci in frontend before launching the test environment.")
    backend = {
        **BASE_ENV, "API_ADDR": "127.0.0.1:8082",
        "MYSQL_HOST": "127.0.0.1", "MYSQL_PORT": "3336", "MYSQL_DATABASE": "mitemau_test",
        "MYSQL_USER": "mitemau_test", "MYSQL_PASSWORD": DB_PASSWORD,
        "APP_ORIGIN": APP, "WEBAUTHN_RP_ID": HOST, "COOKIE_SECURE": "false",
        "EMAIL_CODE_SECRET": secrets.token_hex(32), "RELAY_AUTH_KEY": secrets.token_hex(32),
        "SMTP_HOST": "127.0.0.1", "SMTP_PORT": "1125", "SMTP_REQUIRE_TLS": "false",
        "STAFF_ACCOUNT_IDS": ACCOUNTS["staff"], "TRUST_LOOPBACK_PROXY": "true",
    }
    processes = []
    # Reserve the picker port before starting the stack. Keep everything loopback-only.
    with ThreadingHTTPServer(("127.0.0.1", 5176), Picker) as picker, tempfile.TemporaryDirectory() as tmp:
        binary = str(Path(tmp) / "backend")
        subprocess.run([go, "build", "-o", binary, "."], cwd=ROOT / "backend", env=BASE_ENV, check=True)
        try:
            compose("up", "-d", "--wait", "mysql", "mailpit")
            compose("run", "--rm", "migrate", "-path=/migrations",
                    f"-database=mysql://mitemau_test:{DB_PASSWORD}@tcp(mysql:3306)/mitemau_test", "up")
            seed()
            processes.append(subprocess.Popen([binary], env=backend, cwd=ROOT / "backend"))
            wait_for("http://127.0.0.1:8082/health", processes[-1])
            processes.append(subprocess.Popen(
                ["node", str(vite), "--host", "127.0.0.1", "--port", "5175", "--strictPort"],
                cwd=ROOT / "frontend",
                env={**BASE_ENV, "API_PROXY_TARGET": "http://127.0.0.1:8082", "VITE_DISABLE_RELAY": "true"},
            ))
            wait_for("http://127.0.0.1:5175", processes[-1])
            print(f"Test accounts: {PICKER}\nTest app: {APP}\nTest inbox: http://localhost:8125", flush=True)
            picker.serve_forever()
        finally:
            for process in reversed(processes):
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
            subprocess.run(COMPOSE + ["down", "--remove-orphans"], env=COMPOSE_ENV, cwd=ROOT, check=False)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, lambda *_: signal.raise_signal(signal.SIGINT))
    try:
        main()
    except KeyboardInterrupt:
        pass
