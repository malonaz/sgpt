go_service_manifest(
    name = "manifest",
    visibility = ["PUBLIC"],
)

go_service_src(
    name = "service.tmpl.go",
    manifest = ":manifest",
)

go_copy(
    name = "pb_rpc.go",
    src = "//chat/chat_service/v1:rpc",
)

go_library(
    name = "chat_service",
    srcs = [
        "create_chats.go",
        "search_chats.go",
        "update_chat.go",
        ":pb_rpc.go",
        ":service.tmpl.go",
    ],
    visibility = ["//..."],
    deps = [
        "//chat/chat_service/v1",
        "//chat/v1",
        "//chat/v1/db",
        "//chat/v1/model",
        "//third_party/go:github.com__malonaz__core__go__aip",
        "//third_party/go:github.com__malonaz__core__go__grpc",
        "//third_party/go:github.com__malonaz__core__go__uuid",
        "//third_party/go:go.einride.tech__aip__resourcename",
        "//third_party/go:google.golang.org__grpc__codes",
        "//third_party/go:google.golang.org__grpc__metadata",
        "//third_party/go:google.golang.org__grpc__status",
        "//third_party/go:google.golang.org__protobuf__proto",
        "//third_party/go:google.golang.org__protobuf__types__known__timestamppb",
    ],
)
