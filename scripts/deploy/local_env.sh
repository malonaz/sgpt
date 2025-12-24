#!/bin/bash
set -e

ansible-vault view scripts/deploy/secrets.yml --vault-password-file /secrets/malonaz/ansible-vault-password | \
  python3 -c "
import sys, yaml
data = yaml.safe_load(sys.stdin)
mappings = {
    'EXTERNAL_API_KEY_AUTHENTICATION_API_KEYS': 'external_authentication_api_keys',
    'INTERNAL_SERVICE_AUTHENTICATION_INTERNAL_SERVICE_SECRET': 'internal_authentication_secret',
    'SESSION_MANAGER_SECRET': 'session_manager_secret',
    'AI_SERVICE_ANTHROPIC_API_KEY': 'anthropic_api_key',
    'AI_SERVICE_GROQ_API_KEY': 'groq_api_key',
    'AI_SERVICE_GOOGLE_API_KEY': 'google_api_key',
    'AI_SERVICE_XAI_API_KEY': 'xai_api_key',
    'AI_SERVICE_OPENAI_API_KEY': 'openai_api_key',
    'AI_SERVICE_CEREBRAS_API_KEY': 'cerebras_api_key',
}
for env_var, yaml_key in mappings.items():
    print(f'{env_var}={data.get(yaml_key, \"\")}')
" > .env

echo "Created .env"
