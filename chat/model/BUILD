go_proto_template_library(
    name = "codegen",
    srcs = ["//chat/v1:proto"],
    template = "///core//build_defs/codegen/go_proto_templates/templates:model",
    deps = ["//chat/v1"],
)

go_library(
    name = "model",
    srcs = [
        "doc.go",
        ":codegen",
    ],
    visibility = ["//chat/..."],
    deps = [
        "//chat/v1",
        "//third_party/go:github.com__malonaz__core__go__pbutil",
        "//third_party/go:go.einride.tech__aip__resourcename",
        "//third_party/go:google.golang.org__protobuf__types__known__timestamppb",
    ],
)
