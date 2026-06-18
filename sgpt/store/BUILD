go_protoc_gen_core(
    name = "codegen",
    srcs = ["//sgpt/v1:proto"],
    additional_context = {
        GO_PROTOC_GEN_CORE_GO_IMPORT_PATHS: {
            "model": "//sgpt/model",
        },
    },
    plugin = "postgres",
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
