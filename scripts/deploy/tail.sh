#!/bin/bash
set -e

ssh h-malonaz "journalctl -u sgpt-server -f -o cat"
