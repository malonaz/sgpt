filegroup(
    name = "proto",
    srcs = [
        "configuration.proto",
        "tool.proto",
    ],
    visibility = ["PUBLIC"],
)

proto_library(
    name = "v1",
    srcs = [
        "configuration.proto",
        "tool.proto",
    ],
    additional_context = {
        GENERATE_GO_AIP: True,
        GO_IMPORT_PATH: "github.com/malonaz/sgpt/genproto/sgpt/v1",
    },
    labels = ["copy_generated_code::genproto"],
    visibility = ["PUBLIC"],
    deps = [
        "//third_party/proto:google__api__annotations",
        "//third_party/proto:malonaz__core__genproto__ai__ai_engine__v1",
        "//third_party/proto:validate",
    ],
)
