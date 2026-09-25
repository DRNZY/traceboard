#!/bin/sh
set -eu

dependency_line=$(awk '$1 == "go-build:" { print; exit }' Makefile)
if [ "$dependency_line" != "go-build: web-build" ]; then
  printf '%s\n' "expected go-build to depend on web-build" >&2
  exit 1
fi
