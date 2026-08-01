#!/usr/bin/env python3
"""Render docs/manual.md to docs/kb2040ctl-manual.pdf.

Kept as a script rather than a checked-in one-off so the PDF can be regenerated whenever the
manual changes, and so the styling lives in version control with the text.

Requires pandoc (markdown -> HTML) and WeasyPrint (HTML -> PDF):

    pip install -r requirements-dev.txt
    winget install --id JohnMacFarlane.Pandoc      # or apt/brew install pandoc

Usage:

    python scripts/build-manual.py [-o OUTPUT.pdf]
"""

import argparse
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile

REPO = pathlib.Path(__file__).resolve().parent.parent
SOURCE = REPO / "docs" / "manual.md"
DEFAULT_OUTPUT = REPO / "docs" / "kb2040ctl-manual.pdf"

# Print styling. Deliberately restrained: this is a reference manual, so the priorities are
# a readable measure, tables that never overflow the page, and code that stays legible.
CSS = """
@page {
  size: A4;
  margin: 20mm 18mm 18mm 18mm;
  @bottom-center {
    content: counter(page);
    font-family: "Segoe UI", "Helvetica Neue", Arial, sans-serif;
    font-size: 8.5pt;
    color: #8a8f98;
  }
  @bottom-right {
    content: "kb2040ctl";
    font-family: "Segoe UI", "Helvetica Neue", Arial, sans-serif;
    font-size: 8pt;
    color: #b0b5bd;
  }
}
@page :first { @bottom-center { content: none; } @bottom-right { content: none; } }

html { font-size: 10.5pt; }
body {
  font-family: "Segoe UI", "Helvetica Neue", Arial, sans-serif;
  line-height: 1.5;
  color: #1c1f24;
  hyphens: none;
}

h1 {
  font-size: 19pt;
  margin: 0 0 0.6em;
  padding-bottom: 0.25em;
  border-bottom: 2.5pt solid #2f6feb;
  color: #14181d;
  break-before: page;
  break-after: avoid;
}
h1:first-of-type { break-before: avoid; }
h2 {
  font-size: 14pt;
  margin: 1.4em 0 0.4em;
  color: #14181d;
  break-after: avoid;
}
h3 { font-size: 11.5pt; margin: 1.1em 0 0.35em; break-after: avoid; }
p { margin: 0.5em 0; }

code {
  font-family: "Cascadia Mono", Consolas, "DejaVu Sans Mono", monospace;
  font-size: 0.88em;
  background: #f3f4f6;
  padding: 0.08em 0.3em;
  border-radius: 2pt;
}
pre {
  background: #f7f8fa;
  border: 0.5pt solid #dfe2e7;
  border-left: 2.5pt solid #2f6feb;
  border-radius: 2pt;
  padding: 7pt 9pt;
  margin: 0.7em 0;
  break-inside: avoid;
  white-space: pre-wrap;
  word-wrap: break-word;
}
pre code { background: none; padding: 0; font-size: 8.6pt; line-height: 1.35; }

table {
  border-collapse: collapse;
  width: 100%;
  margin: 0.7em 0;
  font-size: 9.2pt;
  break-inside: avoid;
}
th {
  background: #eef2f8;
  text-align: left;
  font-weight: 600;
  border-bottom: 1pt solid #c4ccd8;
}
th, td {
  padding: 3.5pt 6pt;
  vertical-align: top;
  border-bottom: 0.5pt solid #e4e7ec;
}
td code { font-size: 0.86em; white-space: nowrap; }

blockquote {
  margin: 0.8em 0;
  padding: 6pt 10pt;
  background: #fff8e6;
  border-left: 2.5pt solid #d99e1f;
  break-inside: avoid;
}
blockquote p { margin: 0.2em 0; }

ul, ol { margin: 0.5em 0; padding-left: 1.4em; }
li { margin: 0.2em 0; }
strong { font-weight: 600; }

/* Title block */
.title-block {
  break-after: page;
  padding-top: 55mm;
  text-align: center;
}
.title-block .name {
  font-family: "Cascadia Mono", Consolas, monospace;
  font-size: 34pt;
  font-weight: 600;
  color: #14181d;
  letter-spacing: -0.5pt;
}
.title-block .rule {
  width: 42mm;
  height: 2.5pt;
  background: #2f6feb;
  margin: 7mm auto;
}
.title-block .subtitle { font-size: 12.5pt; color: #4a5057; max-width: 115mm; margin: 0 auto; }
.title-block .meta { margin-top: 16mm; font-size: 9.5pt; color: #8a8f98; line-height: 1.8; }
"""


def build(output: pathlib.Path) -> pathlib.Path:
    for tool, hint in (("pandoc", "install pandoc"),):
        if shutil.which(tool) is None:
            sys.exit("%s is not installed (%s)" % (tool, hint))
    try:
        from weasyprint import HTML  # noqa: PLC0415 - optional, and only needed here
    except ImportError:
        sys.exit("WeasyPrint is not installed. Run: pip install -r requirements-dev.txt")

    text = SOURCE.read_text(encoding="utf-8")
    meta, body = _split_front_matter(text)

    fragment = subprocess.run(
        ["pandoc", "--from", "gfm", "--to", "html5"],
        input=body, capture_output=True, text=True, encoding="utf-8", check=True,
    ).stdout

    html = _page(meta, fragment)

    with tempfile.TemporaryDirectory() as tmp:
        css_path = pathlib.Path(tmp) / "manual.css"
        css_path.write_text(CSS, encoding="utf-8")
        output.parent.mkdir(parents=True, exist_ok=True)
        HTML(string=html, base_url=str(REPO)).write_pdf(output, stylesheets=[str(css_path)])

    return output


def _split_front_matter(text):
    """Return (metadata dict, body). The front matter keeps the title with the source."""
    match = re.match(r"^---\n(.*?)\n---\n(.*)$", text, re.DOTALL)
    if not match:
        return {}, text
    meta = {}
    for line in match.group(1).splitlines():
        key, sep, value = line.partition(":")
        if sep:
            meta[key.strip()] = value.strip()
    return meta, match.group(2)


def _page(meta, fragment):
    title = meta.get("title", "kb2040ctl")
    return """<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>%s manual</title></head><body>
<div class="title-block">
  <div class="name">%s</div>
  <div class="rule"></div>
  <div class="subtitle">%s</div>
  <div class="meta">Version %s<br>Adafruit KB2040 &middot; CircuitPython<br>
    github.com/JeremyProffittOrg/kb2040-single-key</div>
</div>
%s
</body></html>""" % (
        title, title, meta.get("subtitle", ""), meta.get("version", "0.1.0"), fragment,
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("-o", "--output", type=pathlib.Path, default=DEFAULT_OUTPUT,
                        help="where to write the PDF (default: docs/kb2040ctl-manual.pdf)")
    args = parser.parse_args()

    written = build(args.output)
    print("wrote %s (%d KB)" % (written, written.stat().st_size // 1024))


if __name__ == "__main__":
    main()
