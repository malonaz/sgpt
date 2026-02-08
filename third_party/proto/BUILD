package(default_visibility = ["PUBLIC"])

######################### BUF ########################
github_proto_library(
    name = "validate",
    additional_context = {GO_IMPORT_PATH: "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"},
    dir = "proto/protovalidate/buf/validate",
    files = ["validate.proto"],
    repo = "bufbuild/protovalidate",
    revision = "v1.0.0",
    root_dir = "proto/protovalidate",
)

######################### GOOGLEAPIS ########################
GOOGLEAPI_REVISION = "9a477cd3c26a704130e2a2fb44a40281d9312e4c"

github_proto_library(
    name = "google__api",
    additional_context = {GO_IMPORT_PATH: "google.golang.org/genproto/googleapis/api"},
    dir = "google/api",
    files = ["launch_stage.proto"],
    repo = "googleapis/googleapis",
    revision = GOOGLEAPI_REVISION,
)

github_proto_library(
    name = "google__api__annotations",
    additional_context = {GO_IMPORT_PATH: "google.golang.org/genproto/googleapis/api/annotations"},
    dir = "google/api",
    files = [
        "annotations.proto",
        "client.proto",
        "field_behavior.proto",
        "http.proto",
        "resource.proto",
    ],
    repo = "googleapis/googleapis",
    revision = GOOGLEAPI_REVISION,
    deps = [":google__api"],
)

github_proto_library(
    name = "google__api__expr__v1alpha1",
    additional_context = {GO_IMPORT_PATH: "google.golang.org/genproto/googleapis/api/expr/v1alpha1"},
    dir = "google/api/expr/v1alpha1",
    files = [
        "checked.proto",
        "eval.proto",
        "explain.proto",
        "syntax.proto",
        "value.proto",
    ],
    repo = "googleapis/googleapis",
    revision = GOOGLEAPI_REVISION,
    deps = [":google__rpc__status"],
)

github_proto_library(
    name = "google__rpc__status",
    additional_context = {GO_IMPORT_PATH: "google.golang.org/genproto/googleapis/rpc/status"},
    dir = "google/rpc",
    files = ["status.proto"],
    repo = "googleapis/googleapis",
    revision = GOOGLEAPI_REVISION,
)

github_proto_library(
    name = "google__rpc__errdetails",
    additional_context = {GO_IMPORT_PATH: "google.golang.org/genproto/googleapis/rpc/errdetails"},
    dir = "google/rpc",
    files = ["error_details.proto"],
    repo = "googleapis/googleapis",
    revision = GOOGLEAPI_REVISION,
)

######################### MALONAZ ########################

github_proto_library(
    name = "malonaz__core__genproto__authentication__v1",
    additional_context = {GO_IMPORT_PATH: "github.com/malonaz/core/genproto/authentication/v1"},
    dir = "malonaz/authentication/v1",
    files = ["authentication.proto"],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [":validate"],
)

github_proto_library(
    name = "malonaz__core__genproto__audio__v1",
    additional_context = {GO_IMPORT_PATH: "github.com/malonaz/core/genproto/audio/v1"},
    dir = "malonaz/audio/v1",
    files = ["audio.proto"],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [":validate"],
)

github_proto_library(
    name = "malonaz__core__genproto__json__v1",
    additional_context = {GO_IMPORT_PATH: "github.com/malonaz/core/genproto/json/v1"},
    dir = "malonaz/json/v1",
    files = ["schema.proto"],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [":validate"],
)

github_proto_library(
    name = "malonaz__core__genproto__ai__v1",
    additional_context = {
        GO_IMPORT_PATH: "github.com/malonaz/core/genproto/ai/v1",
        GENERATE_GO_AIP: True,
    },
    dir = "malonaz/ai/v1",
    files = [
        "message.proto",
        "metrics.proto",
        "model.proto",
        "tool.proto",
        "voice.proto",
    ],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [
        ":google__api__annotations",
        ":google__rpc__status",
        ":malonaz__core__genproto__audio__v1",
        ":malonaz__core__genproto__json__v1",
        ":validate",
    ],
)

github_proto_library(
    name = "malonaz__core__genproto__grpc__v1",
    additional_context = {
        GO_IMPORT_PATH: "github.com/malonaz/core/genproto/grpc/v1",
        GENERATE_GO_AIP: True,
    },
    dir = "malonaz/grpc/v1",
    files = [
        "grpc.proto",
    ],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [
    ],
)

github_grpc_library(
    name = "malonaz__core__genproto__ai__ai_service__v1",
    additional_context = {GO_IMPORT_PATH: "github.com/malonaz/core/genproto/ai/ai_service/v1"},
    dir = "malonaz/ai/ai_service/v1",
    files = [
        "ai_service.proto",
        "model.proto",
        "speech_to_text.proto",
        "text_to_speech.proto",
        "text_to_text.proto",
        "voice.proto",
    ],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [
        ":google__api__annotations",
        ":malonaz__core__genproto__ai__v1",
        ":malonaz__core__genproto__audio__v1",
        ":malonaz__core__genproto__codegen__aip__v1",
        ":validate",
    ],
)

github_grpc_library(
    name = "malonaz__core__genproto__ai__ai_engine__v1",
    additional_context = {GO_IMPORT_PATH: "github.com/malonaz/core/genproto/ai/ai_engine/v1"},
    dir = "malonaz/ai/ai_engine/v1",
    files = ["ai_engine.proto"],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [
        ":google__api__annotations",
        ":malonaz__core__genproto__ai__v1",
        ":validate",
    ],
)

github_proto_library(
    name = "malonaz__core__genproto__codegen__model__v1",
    additional_context = {GO_IMPORT_PATH: "github.com/malonaz/core/genproto/codegen/model/v1"},
    dir = "malonaz/codegen/model/v1",
    files = ["model.proto"],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [":validate"],
)

github_proto_library(
    name = "malonaz__core__genproto__codegen__gateway__v1",
    additional_context = {GO_IMPORT_PATH: "github.com/malonaz/core/genproto/codegen/gateway/v1"},
    dir = "malonaz/codegen/gateway/v1",
    files = ["gateway.proto"],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [":validate"],
)

github_proto_library(
    name = "malonaz__core__genproto__codegen__aip__v1",
    additional_context = {GO_IMPORT_PATH: "github.com/malonaz/core/genproto/codegen/aip/v1"},
    dir = "malonaz/codegen/aip/v1",
    files = ["aip.proto"],
    repo = "malonaz/core",
    revision = CONFIG.CORE_REVISION,
    deps = [":validate"],
)
