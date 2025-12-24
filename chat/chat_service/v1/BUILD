filegroup(
    name = "proto",
    srcs = [
        "chat_service.proto",
    ],
    visibility = ["//chat/chat_service/..."],
)

grpc_library(
    name = "v1",
    srcs = ["chat_service.proto"],
    additional_context = {
        GO_IMPORT_PATH: "github.com/malonaz/sgpt/genproto/chat/chat_service/v1",
    },
    labels = ["copy_generated_code"],
    visibility = [
        "//chat/chat_service/...",
        "//cli/...",
        "//cmd/server/...",
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
