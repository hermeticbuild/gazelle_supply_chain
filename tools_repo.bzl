"""Repository rule that builds Gazelle's go_repository tools with the license plugin.

go_repository always runs the Gazelle binary from
@bazel_gazelle_go_repository_tools. Overriding that repo (see
`override_repo` in the example MODULE.bazel) makes every go_deps repository
run a Gazelle that also knows the `license_default` / `license_subtree`
directives.

The binaries are built with the host `go` (the same constraint the stock
tools repo has), using `go build -overlay` so the plugin and the language list
are injected into the Gazelle module without modifying it.
"""

_BUILD = """\
package(default_visibility = ["//visibility:public"])

filegroup(name = "fetch_repo", srcs = ["bin/fetch_repo"])

filegroup(name = "gazelle", srcs = ["bin/gazelle"])

filegroup(name = "generate_repo_config", srcs = ["bin/generate_repo_config"])

exports_files(["ROOT"])
"""

_LANGS = """\
package main

import (
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/language/bazel/visibility"
	golang "github.com/bazelbuild/bazel-gazelle/language/go"
	"github.com/bazelbuild/bazel-gazelle/language/license"
	"github.com/bazelbuild/bazel-gazelle/language/proto"
)

var languages = []language.Language{
	visibility.NewLanguage(),
	proto.NewLanguage(),
	golang.NewLanguage(),
	license.NewLanguage(),
}
"""

def _impl(ctx):
    go = ctx.which("go")
    if not go:
        fail("A host `go` binary is required to build the Gazelle tools.")

    # -overlay keys must match the real path Go sees, so resolve symlinks.
    resolved = ctx.execute(["sh", "-c", 'cd "$1" && pwd -P', "sh", str(ctx.path(Label("@gazelle//:MODULE.bazel")).dirname)])
    if resolved.return_code:
        fail("failed to resolve the gazelle source dir: " + resolved.stderr)
    gazelle_dir = resolved.stdout.strip()
    plugin = ctx.path(Label("//:language.go"))
    ctx.watch(plugin)

    ctx.file("overlay/langs.go", _LANGS)
    ctx.file("overlay.json", json.encode({"Replace": {
        gazelle_dir + "/cmd/gazelle/langs.go": str(ctx.path("overlay/langs.go")),
        gazelle_dir + "/language/license/language.go": str(plugin),
    }}))

    for tool in ["gazelle", "fetch_repo", "generate_repo_config"]:
        result = ctx.execute(
            [
                go,
                "build",
                "-overlay",
                str(ctx.path("overlay.json")),
                "-o",
                str(ctx.path("bin/" + tool)),
                "./cmd/" + tool,
            ],
            working_directory = gazelle_dir,
            environment = {"CGO_ENABLED": "0"},
        )
        if result.return_code:
            fail("failed to build %s: %s" % (tool, result.stderr))

    ctx.file("BUILD.bazel", _BUILD)
    ctx.file("ROOT", "")

gazelle_tools_with_license = repository_rule(
    _impl,
    environ = ["PATH", "GOPROXY", "GOCACHE", "GOPATH"],
    doc = "Builds a Gazelle (go, proto, visibility, license) in the layout go_repository expects.",
)
