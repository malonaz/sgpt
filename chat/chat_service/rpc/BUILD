go_proto_template_library(
    name = "codegen",
    srcs = ["//chat/chat_service/v1:proto"],
    additional_context = {
        GO_PROTO_TEMPLATES_GO_IMPORT_PATHS: {
            "model": "//chat/model",
        },
    },
    template = "///core//build_defs/codegen/go_proto_templates/templates:rpc",
    deps = ["//chat/chat_service/v1"],
)

go_library(
    name = "rpc",
    srcs = [":codegen"],
    visibility = ["//chat/chat_service/..."],
    deps = [
        "//chat/chat_service/v1",
        "//chat/model",
        "//chat/v1",
        "//third_party/go:github.com__malonaz__core__go__aip",
        "//third_party/go:github.com__malonaz__core__go__uuid",
        "//third_party/go:go.einride.tech__aip__resourcename",
        "//third_party/go:google.golang.org__grpc__codes",
        "//third_party/go:google.golang.org__grpc__metadata",
        "//third_party/go:google.golang.org__grpc__status",
        "//third_party/go:google.golang.org__protobuf__types__known__timestamppb",
    ],
)
