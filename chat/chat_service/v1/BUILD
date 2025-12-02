grpc_library(
    name = "v1",
    srcs = ["chat_service.proto"],
    additional_context = {
        GO_IMPORT_PATH: "github.com/malonaz/sgpt/chat/chat_service/v1",
    },
    visibility = [
        "//chat/chat_service/...",
        "//cli/...",
        "//engine/engine_service/...",
        "//gateway/...",
        "//server/...",
    ],
    deps = [
        "//chat/v1",
        "//third_party/proto:google__api__annotations",
        "//third_party/proto:google__api__expr__v1alpha1",
        "//third_party/proto:malonaz__core__genproto__authentication__v1",
        "//third_party/proto:malonaz__core__genproto__codegen__aip__v1",
        "//third_party/proto:validate",
    ],
)

go_proto_template_library(
    name = "rpc",
    srcs = ["chat_service.proto"],
    additional_context = {
        GO_PROTO_TEMPLATES_PACKAGE_NAME: "chat_service",
    },
    template = "///core//build_defs/codegen/go_proto_templates/templates:rpc",
    visibility = ["//chat/chat_service/..."],
    deps = [
        ":v1",
    ],
)
