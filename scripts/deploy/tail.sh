#!/bin/bash
set -e

n=${1:-50}
ssh h-malonaz "journalctl -u sgpt-server -f -o cat -n $n"
