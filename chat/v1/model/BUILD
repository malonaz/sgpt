go_copy(
    name = "pb_model.go",
    src = "//chat/v1:v1_model",
)

go_library(
    name = "model",
    srcs = [":pb_model.go"],
    visibility = ["//chat/..."],
    deps = [
        "//chat/v1",
        "//third_party/go:github.com__malonaz__core__go__pbutil",
        "//third_party/go:github.com__malonaz__core__go__postgres",
        "//third_party/go:go.einride.tech__aip__resourcename",
        "//third_party/go:google.golang.org__protobuf__types__known__timestamppb",
    ],
)
