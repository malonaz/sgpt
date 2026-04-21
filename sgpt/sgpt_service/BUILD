go_service_manifest(
    name = "manifest",
    visibility = ["PUBLIC"],
)

go_service_src(
    name = "service.tmpl.go",
    manifest = ":manifest",
)

go_library(
    name = "sgpt_service",
    srcs = [
        "create_chats.go",
        "search_chats.go",
        "update_chat.go",
        ":service.tmpl.go",
    ],
    visibility = ["//..."],
    deps = [
        "//sgpt/sgpt_service/rpc",
        "//sgpt/sgpt_service/v1",
        "//sgpt/store",
        "//sgpt/v1",
        "//third_party/go:github.com__malonaz__core__go__aip",
        "//third_party/go:github.com__malonaz__core__go__grpc__status",
        "//third_party/go:google.golang.org__grpc__codes",
    ],
)
