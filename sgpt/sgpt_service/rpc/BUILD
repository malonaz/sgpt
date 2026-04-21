go_proto_template_library(
    name = "codegen",
    srcs = ["//sgpt/sgpt_service/v1:proto"],
    additional_context = {
        GO_PROTO_TEMPLATES_GO_IMPORT_PATHS: {
            "model": "//sgpt/model",
        },
    },
    template = "///core//build_defs/codegen/go_proto_templates/templates:rpc",
    deps = ["//sgpt/sgpt_service/v1"],
)

go_library(
    name = "rpc",
    srcs = [":codegen"],
    visibility = ["//sgpt/sgpt_service/..."],
    deps = [
        "//sgpt/model",
        "//sgpt/sgpt_service/v1",
        "//sgpt/v1",
        "//third_party/go:buf.build__go__protovalidate",
        "//third_party/go:github.com__malonaz__core__go__aip",
        "//third_party/go:github.com__malonaz__core__go__grpc__status",
        "//third_party/go:github.com__malonaz__core__go__uuid",
        "//third_party/go:go.einride.tech__aip__resourcename",
        "//third_party/go:google.golang.org__grpc__codes",
        "//third_party/go:google.golang.org__grpc__metadata",
        "//third_party/go:google.golang.org__grpc__status",
        "//third_party/go:google.golang.org__protobuf__proto",
        "//third_party/go:google.golang.org__protobuf__types__known__timestamppb",
    ],
)
