go_copy(
    name = "pb_db.go",
    src = "//chat/v1:v1_db",
)

go_library(
    name = "db",
    srcs = [
        "chat_searchable_content.go",
        ":pb_db.go",
    ],
    visibility = ["//..."],
    deps = [
        "//chat/v1/model",
        "//third_party/go:github.com__jackc__pgx__v5",
        "//third_party/go:github.com__malonaz__core__go__postgres",
    ],
)
