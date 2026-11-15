#!/usr/bin/env python3
"""Copy the optional visual page starter without overwriting an existing page."""

import argparse
import html
from pathlib import Path
import shlex
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path, help="Page directory; creates index.html")
    parser.add_argument("--title", default="可视化工作页", help="Page title and main heading")
    parser.add_argument("--team", help="Existing Otoha team for the printed publish commands")
    parser.add_argument("--static", action="store_true", help="Print a static preview command instead of Otoha commands")
    args = parser.parse_args()

    template = Path(__file__).resolve().parent.parent / "assets" / "starter.html"
    output_dir = args.output.expanduser().resolve()
    destination = output_dir / "index.html"
    try:
        page = template.read_text(encoding="utf-8").replace("__PAGE_TITLE__", html.escape(args.title))
        output_dir.mkdir(parents=True, exist_ok=True)
        with destination.open("x", encoding="utf-8") as target:
            target.write(page)
    except FileExistsError:
        parser.exit(1, f"Refusing to overwrite existing path: {destination}\n")
    except OSError as exc:
        parser.exit(1, f"Cannot create page: {exc}\n")

    print(destination)
    if args.static:
        print(f"python3 -m http.server 8000 --bind 127.0.0.1 --directory {shlex.quote(str(output_dir))}")
    else:
        team_args = f" --team {shlex.quote(args.team)}" if args.team else ""
        revision = "page-" + uuid.uuid4().hex
        print(f"otoha page open{team_args}")
        print(f"otoha page publish{team_args} --file {shlex.quote(str(destination))} --request-id {revision}")
        print(f"otoha page operation{team_args} --request-id {revision}")


if __name__ == "__main__":
    main()
