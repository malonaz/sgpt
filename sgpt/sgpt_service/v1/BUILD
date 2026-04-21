package(default_visibility = ["PUBLIC"])

filegroup(
    name = "proto",
    srcs = [
        "sgpt_service.proto",
    ],
)

grpc_library(
    name = "v1",
    srcs = ["sgpt_service.proto"],
    additional_context = {
        GO_IMPORT_PATH: "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1",
    },
    labels = ["copy_generated_code"],
    deps = [
        "//sgpt/v1",
        "//third_party/proto:google__api__annotations",
        "//third_party/proto:google__api__expr__v1alpha1",
        "//third_party/proto:malonaz__core__genproto__authentication__v1",
        "//third_party/proto:malonaz__core__genproto__codegen__aip__v1",
        "//third_party/proto:validate",
    ],
)
