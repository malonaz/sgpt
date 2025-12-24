go_main_manifest(
    name = "manifest",
    deps = [
        "//ai/ai_service:manifest",
        "//chat/chat_service:manifest",
        "//chat/chat_service/v1",
        "//third_party/proto:malonaz__core__genproto__ai__ai_service__v1",
    ],
)

go_main_src(
    name = "main.go",
    manifest = ":manifest",
)

go_binary(
    name = "server",
    srcs = [
        "init.go",
        ":main.go",
    ],
    deps = [
        "//chat/chat_service",
        "//chat/chat_service/v1",
        "//chat/store",
        "//third_party/go:github.com__malonaz__core__genproto__ai__ai_service__v1",
        "//third_party/go:github.com__malonaz__core__go__ai__ai_service",
        "//third_party/go:github.com__malonaz__core__go__authentication",
        "//third_party/go:github.com__malonaz__core__go__binary",
        "//third_party/go:github.com__malonaz__core__go__certs",
        "//third_party/go:github.com__malonaz__core__go__flags",
        "//third_party/go:github.com__malonaz__core__go__grpc",
        "//third_party/go:github.com__malonaz__core__go__health",
        "//third_party/go:github.com__malonaz__core__go__logging",
        "//third_party/go:github.com__malonaz__core__go__postgres",
        "//third_party/go:github.com__malonaz__core__go__prometheus",
    ],
)

_databases = [
    "chat",
]

_binary_deps = [f"//{db}/migrations:initializer" for db in _databases] + [f"//{db}/migrations:migrator" for db in _databases] + ["//cmd/server"]

_env = {
    "POSTGRES_HOST": "localhost",
    "POSTGRES_PORT": "8989",
    "CHAT_POSTGRES_HOST": "localhost",
    "CHAT_POSTGRES_PORT": "8989",
    "CHAT_POSTGRES_USER": "chat",
    "CHAT_POSTGRES_DATABASE": "chat",
}

_env_str = " ".join([f"{k}={v}" for k, v in _env.items()])

sh_cmd(
    name = "local",
    cmd = " ".join([
        f"env $(cat .env | xargs) {_env_str} plz-out/bin/cmd/server/server",
        "--logging.format=pretty",
        "--prometheus.disable",
        # Servers.
        "--internal-grpc.disable-tls",
        "--internal-grpc.socket-path=/tmp/core.socket",
        "--external-api-key-authentication.config=cmd/server/authentication/external_service_accounts.json",
        "--internal-service-authentication.config=cmd/server/authentication/internal_service_accounts.json",
        "--permission-authentication.config=cmd/server/authentication/roles.json",
    ]),
    data = _binary_deps,
    expand_env_vars = False,
)
