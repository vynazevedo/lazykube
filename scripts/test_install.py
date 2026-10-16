"""Exercise the installer against an isolated, local HTTP release server."""
import functools
import hashlib
import http.server
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import threading
import zipfile

ROOT = Path(__file__).resolve().parent.parent
VERSION = "v1.2.3"
PAYLOAD = b"#!/bin/sh\nprintf 'fixture binary\\n'\n"


class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/latest":
            self.send_response(302)
            self.send_header("Location", f"/tag/{VERSION}")
            self.end_headers()
        else:
            super().do_GET()

    def log_message(self, *_args):
        pass


with tempfile.TemporaryDirectory(prefix="lazykube-install-test-") as temporary:
    root = Path(temporary)
    assets = root / "download" / VERSION
    assets.mkdir(parents=True)
    (root / "tag").mkdir()
    (root / "tag" / VERSION).write_text(
        f'<a href="/releases/expanded_assets/{VERSION}">Assets</a>\n'
    )
    checksums = []
    for system in ("linux", "darwin", "windows"):
        for arch in ("amd64", "arm64"):
            name = f"lazykube_1.2.3_{system}_{arch}"
            if system == "windows":
                archive = assets / (name + ".zip")
                with zipfile.ZipFile(archive, "w") as output:
                    output.writestr("lazykube.exe", PAYLOAD)
            else:
                archive = assets / (name + ".tar.gz")
                with tarfile.open(archive, "w:gz") as output:
                    info = tarfile.TarInfo("lazykube")
                    info.size = len(PAYLOAD)
                    info.mode = 0o755
                    output.addfile(info, io.BytesIO(PAYLOAD))
            checksums.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
    checksum_file = assets / "checksums.txt"
    checksum_file.write_text("".join(checksums))
    server = http.server.ThreadingHTTPServer(
        ("127.0.0.1", 0), functools.partial(Handler, directory=root)
    )
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        def run(system="Linux", arch="x86_64", *, version=VERSION,
                fallback=False, failure=None, extra=None, directory_target=None):
            directory = root / "installed bin"
            shim = root / "path"
            shim.mkdir(exist_ok=True)
            for entry in shim.iterdir():
                entry.unlink()
            commands = ["bash", "mktemp", "rm", "sed", "head", "awk", "tar", "gzip",
                        "unzip", "mkdir", "cp", "chmod", "mv"]
            commands += ["wget", "shasum"] if fallback else ["curl", "sha256sum"]
            for command in commands:
                executable = shutil.which(command)
                assert executable, f"Missing test dependency: {command}"
                (shim / command).symlink_to(executable)
            uname = shim / "uname"
            uname.write_text(f'#!/bin/sh\ncase "$1" in -s) echo {system};; -m) echo {arch};; esac\n')
            uname.chmod(0o755)
            env = {**os.environ, "PATH": str(shim), "VERSION": version,
                   "BASE_URL": f"http://127.0.0.1:{server.server_port}",
                   "INSTALL_DIR": str(directory), "TMPDIR": str(root)}
            env.update(extra or {})
            binary = directory / ("lazykube.exe" if system.startswith(("MINGW", "MSYS", "CYGWIN")) else "lazykube")
            target = root / "preserved directory"
            if directory_target:
                directory.mkdir(exist_ok=True)
                binary.unlink(missing_ok=True)
                if directory_target == "symlink":
                    target.mkdir()
                    binary.symlink_to(target, target_is_directory=True)
                else:
                    binary.mkdir()
                    target = binary
                (target / "sentinel").write_bytes(b"preserve me")
            before = binary.read_bytes() if binary.is_file() else None
            result = subprocess.run([str(shim / "bash"), str(ROOT / "install.sh")],
                                    env=env, text=True, capture_output=True)
            if failure:
                assert result.returncode != 0, result.stdout
                assert failure in result.stderr, result.stderr
                assert (binary.read_bytes() if binary.is_file() else None) == before
            else:
                assert result.returncode == 0, result.stderr
                assert binary.read_bytes() == PAYLOAD
                assert os.access(binary, os.X_OK)
                assert "SHA-256 verificado" in result.stdout
                assert "ao PATH" in result.stdout
            assert not list(root.glob("tmp.*")), "Temporary directory leaked"
            assert not list(root.rglob(".lazykube.*")), "Staged binary leaked"
            if directory_target:
                assert binary.is_dir()
                assert binary.is_symlink() == (directory_target == "symlink")
                assert list(target.iterdir()) == [target / "sentinel"]
                assert (target / "sentinel").read_bytes() == b"preserve me"
                if binary.is_symlink():
                    assert binary.resolve() == target
                    binary.unlink()
                shutil.rmtree(target)

        for system in ("Linux", "Darwin", "MINGW64_NT-10.0", "MSYS_NT-10.0", "CYGWIN_NT-10.0"):
            for arch in ("x86_64", "aarch64"):
                run(system, arch)
        run(version="")
        run(version="", fallback=True)
        run(fallback=True)
        run("FreeBSD", failure="Sistema nao suportado")
        run(arch="riscv64", failure="Arquitetura nao suportada")
        run(version="../../bad", failure="Versao invalida")
        run(directory_target="directory", failure="Destino e um diretorio")
        run(directory_target="symlink", failure="Destino e um diretorio")
        script = (ROOT / "install.sh").read_bytes()
        invocation = script.rindex(b'\nmain "$@"') + 1
        offsets = {0, 1, 3, invocation}
        offsets.update(index + 1 for index, byte in enumerate(script[:invocation]) if byte == 10)
        offsets.update(range(0, invocation, 37))
        for offset in sorted(offsets):
            destination = root / "truncated install"
            result = subprocess.run(
                [shutil.which("bash")], input=script[:offset], capture_output=True,
                env={**os.environ, "VERSION": VERSION,
                     "BASE_URL": f"http://127.0.0.1:{server.server_port}",
                     "INSTALL_DIR": str(destination), "TMPDIR": str(root)},
            )
            assert not destination.exists(), f"Truncated script installed at offset {offset}"
            assert b"Instalado em" not in result.stdout
            assert not list(root.glob("tmp.*")), f"Temporary directory leaked at offset {offset}"
            assert not list(root.rglob(".lazykube.*")), f"Staging leaked at offset {offset}"
        checksum_file.write_text("")
        run(failure="Checksum ausente")
        checksum_file.write_text(checksums[0] + checksums[0])
        run(failure="Checksum ausente")
        checksum_file.write_text("0" * 64 + "  lazykube_1.2.3_linux_amd64.tar.gz\n")
        run(failure="SHA-256 divergente")
        checksum_file.write_text("".join(checksums))
        with (assets / "lazykube_1.2.3_linux_amd64.tar.gz").open("ab") as output:
            output.write(b"tampered")
        run(failure="SHA-256 divergente")
        run(version="v9.9.9", failure="404")
        print(f"Installer: 23 local cases and {len(offsets)} truncation offsets passed (preservation and cleanup included).")
    finally:
        server.shutdown()
        server.server_close()
