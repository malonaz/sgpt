filegroup(
    name = "proto",
    srcs = [
        "chat.proto",
        "configuration.proto",
    ],
    visibility = ["PUBLIC"],
)

proto_library(
    name = "v1",
    srcs = [
        "chat.proto",
        "configuration.proto",
    ],
    additional_context = {
        GENERATE_GO_AIP: True,
        GO_IMPORT_PATH: "github.com/malonaz/sgpt/genproto/sgpt/v1",
    },
    labels = ["copy_generated_code::genproto"],
    visibility = ["PUBLIC"],
    deps = [
        "//third_party/proto:google__api__annotations",
        "//third_party/proto:google__rpc__status",
        "//third_party/proto:malonaz__core__genproto__ai__v1",
        "//third_party/proto:malonaz__core__genproto__codegen__model__v1",
        "//third_party/proto:malonaz__core__genproto__json__v1",
        "//third_party/proto:validate",
    ],
)
