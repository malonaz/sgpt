go_proto_template_library(
    name = "codegen",
    srcs = ["//sgpt/v1:proto"],
    additional_context = {
        GO_PROTO_TEMPLATES_GO_IMPORT_PATHS: {
            "model": "//sgpt/model",
        },
    },
    template = "///core//build_defs/codegen/go_proto_templates/templates:postgres",
    deps = ["//sgpt/v1"],
)

go_library(
    name = "store",
    srcs = [
        "chat_searchable_content.go",
        ":codegen",
    ],
    visibility = ["//..."],
    deps = [
        "//sgpt/model",
        "//third_party/go:github.com__jackc__pgx__v5",
        "//third_party/go:github.com__malonaz__core__go__postgres",
    ],
)
