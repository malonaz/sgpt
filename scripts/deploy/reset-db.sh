#!/bin/bash
set -e

ssh h-malonaz "set -a && source /etc/sgpt-server/.env && set +a && /usr/local/bin/chat-postgres-reset"
