proto_library(
    name = "v1",
    srcs = ["chat.proto"],
    additional_context = {
        GENERATE_GO_AIP: True,
        GO_IMPORT_PATH: "github.com/malonaz/sgpt/chat/v1",
    },
    visibility = [
        "//chat/...",
        "//cli/...",
        "//engine/...",
        "//gateway/...",
    ],
    deps = [
        "//third_party/proto:google__api__annotations",
        "//third_party/proto:malonaz__core__genproto__ai__v1",
        "//third_party/proto:malonaz__core__genproto__codegen__llm__v1",
        "//third_party/proto:malonaz__core__genproto__codegen__model__v1",
        "//third_party/proto:validate",
    ],
)

go_proto_template_library(
    name = "v1_model",
    srcs = ["chat.proto"],
    additional_context = {
        GO_IMPORT_PATH: "github.com/malonaz/sgpt/chat/v1/model",
    },
    template = "///core//build_defs/codegen/go_proto_templates/templates:model",
    visibility = [
        "//chat/...",
    ],
    deps = [":v1"],
)

go_proto_template_library(
    name = "v1_db",
    srcs = ["chat.proto"],
    additional_context = {
        GO_IMPORT_PATH: "github.com/malonaz/sgpt/chat/v1/db",
    },
    template = "///core//build_defs/codegen/go_proto_templates/templates:db",
    visibility = [
        "//chat/...",
    ],
    deps = [":v1"],
)
