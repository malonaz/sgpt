GODEPS_VERSION = "v0.0.33"

go_module(
    name = "godeps",
    binary = True,
    install = ["cmd/godeps"],
    module = "github.com/sagikazarmark/please-go-modules",
    patch = "godeps.patch",
    version = GODEPS_VERSION,
    deps = [
        "//third_party/go:github.com__bazelbuild__buildtools__build",
        "//third_party/go:github.com__scylladb__go-set__strset",
    ],
)

WOLLEMI_VERSION = "v0.8.1"

remote_file(
    name = "wollemi",
    binary = True,
    exported_files = ["wollemi"],
    extract = True,
    url = f"https://github.com/tcncloud/wollemi/releases/download/{WOLLEMI_VERSION}/wollemi-{WOLLEMI_VERSION}-{CONFIG.HOSTOS}-{CONFIG.HOSTARCH}.tar.gz",
)

def echo_message(message):
    return f'echo "###### {message} ######"'

sh_cmd(
    name = "tidy",
    cmd = [
        echo_message("Running 'go mod tidy'"),
        "go mod tidy",
        "export GOROOT=\\\\$($(out_exe //tools:go_toolchain|go) env GOROOT)",
        echo_message("Running 'go deps'"),
        "$(out_exe :godeps) -dir third_party/go -builtin -wollemi -arm",
        echo_message("Running 'wollemi'"),
        "$(out_exe :wollemi) gofmt ./...",
        echo_message("Running 'gofmt'"),
        f"gofmt -s -w \\\\$(go list ./... | sed s#{CONFIG.GO_IMPORT_PATH}/##g)",
    ],
    deps = [
        ":godeps",
        ":wollemi",
        "//tools:go_toolchain",
    ],
)
