filegroup(
    name = "proto",
    srcs = [
        "configuration.proto",
        "graph.proto",
        "labels.proto",
        "tool.proto",
        "tools.proto",
    ],
    visibility = ["PUBLIC"],
)

# Descriptor set with source info, embedded in the binary so tool schemas can
# be built via reflection with proto comments as descriptions.
proto_descriptor_set(
    name = "descriptor_set",
    srcs = [":v1"],
    protoc_flags = ["-Ithird_party/proto"],
    visibility = ["PUBLIC"],
    deps = [
        ":v1",
        "//third_party/proto:google__api__annotations",
        "//third_party/proto:malonaz__core__genproto__ai__ai_engine__v1",
        "//third_party/proto:malonaz__core__genproto__codegen__aip__v1",
        "//third_party/proto:validate",
    ],
)

proto_library(
    name = "v1",
    srcs = [
        "configuration.proto",
        "graph.proto",
        "labels.proto",
        "tool.proto",
        "tools.proto",
    ],
    additional_context = {
        GENERATE_GO_AIP: True,
        GENERATE_GO_AIP_LABEL: True,
        GO_IMPORT_PATH: "github.com/malonaz/sgpt/genproto/sgpt/v1",
    },
    labels = ["copy_generated_code::genproto"],
    visibility = ["PUBLIC"],
    deps = [
        "//third_party/proto:google__api__annotations",
        "//third_party/proto:malonaz__core__genproto__ai__ai_engine__v1",
        "//third_party/proto:malonaz__core__genproto__codegen__aip__v1",
        "//third_party/proto:validate",
    ],
)
