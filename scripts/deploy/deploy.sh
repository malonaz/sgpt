#!/bin/bash
set -e

echo "Building..."
plz build //cmd/server

echo "Deploying..."
ansible-playbook -i scripts/deploy/inventory.ini scripts/deploy/playbook.yml --vault-password-file /secrets/malonaz/ansible-vault-password
