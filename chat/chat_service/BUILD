go_service_manifest(
    name = "manifest",
    visibility = ["PUBLIC"],
)

go_service_src(
    name = "service.tmpl.go",
    manifest = ":manifest",
)

go_library(
    name = "chat_service",
    srcs = [
        "create_chats.go",
        "search_chats.go",
        "update_chat.go",
        ":service.tmpl.go",
    ],
    visibility = ["//..."],
    deps = [
        "//chat/chat_service/rpc",
        "//chat/chat_service/v1",
        "//chat/store",
        "//chat/v1",
        "//third_party/go:github.com__malonaz__core__go__aip",
        "//third_party/go:github.com__malonaz__core__go__grpc",
        "//third_party/go:google.golang.org__grpc__codes",
    ],
)
