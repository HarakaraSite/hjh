#!/usr/bin/env python3
"""Compatibility entry point for the Go Linux/amd64 packager."""

import os
from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parent.parent
args = sys.argv[1:]
caller_directory = Path.cwd()
for name in ("--out", "--release-dir"):
    for index, argument in enumerate(args):
        if argument == name and index + 1 < len(args):
            args[index + 1] = str((caller_directory / args[index + 1]).resolve())
        elif argument.startswith(name + "="):
            value = argument.partition("=")[2]
            args[index] = f"{name}={(caller_directory / value).resolve()}"

os.execvp("go", ["go", "-C", str(ROOT), "run", "./scripts/package-linux", *args])
