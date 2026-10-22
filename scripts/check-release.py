import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request


def check_release():
    tag = urllib.parse.quote(os.environ["RELEASE_TAG"], safe="")
    repo = os.environ["GITHUB_REPOSITORY"]
    api = os.environ.get("GITHUB_API_URL", "https://api.github.com").rstrip("/")
    request = urllib.request.Request(
        f"{api}/repos/{repo}/releases/tags/{tag}",
        headers={
            "Authorization": f"Bearer {os.environ['GH_TOKEN']}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            if response.status != 200:
                raise ValueError("resposta inesperada da API")
            release = json.load(response)
    except urllib.error.HTTPError as error:
        if error.code == 404:
            print("Release ausente; criacao permitida.")
            return
        raise ValueError(f"API recusou consulta: HTTP {error.code}") from None
    if not isinstance(release, dict) or release.get("draft") is not True:
        raise ValueError("release publica ou resposta sem draft valido; publicacao recusada")
    print("Release em rascunho; continuacao permitida.")


if __name__ == "__main__":
    try:
        check_release()
    except (KeyError, ValueError, OSError) as error:
        print(f"Verificacao da release falhou: {error}", file=sys.stderr)
        sys.exit(1)
