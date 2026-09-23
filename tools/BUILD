package(default_visibility = ["PUBLIC"])

# Protoc dependency - referenced in .plzconfig.
protoc_binary(
    name = "protoc",
    version = "32.1",
)

# Gathers the proto go dependencies - referenced in .plzconfig.
filegroup(
    name = "protoc-gen-go-dep",
    exported_deps = [
        "//third_party/go:google.golang.org__protobuf__reflect__protoreflect",
        "//third_party/go:google.golang.org__protobuf__runtime__protoimpl",
        "//third_party/go:google.golang.org__protobuf__types__descriptorpb",
        "//third_party/go:google.golang.org__protobuf__types__known__anypb",
        "//third_party/go:google.golang.org__protobuf__types__known__durationpb",
        "//third_party/go:google.golang.org__protobuf__types__known__emptypb",
        "//third_party/go:google.golang.org__protobuf__types__known__fieldmaskpb",
        "//third_party/go:google.golang.org__protobuf__types__known__structpb",
        "//third_party/go:google.golang.org__protobuf__types__known__timestamppb",
    ],
    visibility = ["PUBLIC"],
)

# Gathers the proto go grpc dependencies - referenced in .plzconfig.
filegroup(
    name = "protoc-gen-go-grpc-dep",
    exported_deps = [
        "//third_party/go:google.golang.org__grpc",
        "//third_party/go:google.golang.org__grpc__codes",
        "//third_party/go:google.golang.org__grpc__status",
    ],
    visibility = ["PUBLIC"],
)

# Gathers the proto go aip dependencies - referenced in .plzconfig. Pinned to
# this repo's module set so generated code links against one copy of core.
filegroup(
    name = "protoc-gen-go-aip-dep",
    exported_deps = [
        "//third_party/go:github.com__malonaz__core__go__aip",
        "//third_party/go:go.einride.tech__aip__resourcename",
    ],
    visibility = ["PUBLIC"],
)
