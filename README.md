# gazelle_supply_chain

A Gazelle extension that explicitly tags Bazel packages with a
`default_applicable_licenses` entry, so a repo can carry different licenses for
different subtrees (e.g. MIT everywhere but for subpath of a repo).

## Directive

```starlark
# gazelle:license_default //enterprise:package_metadata
```

- Applies to the directory's BUILD file and every BUILD file beneath it.
- A nested directive overrides the parent; `# gazelle:license_default none` stops tagging for that subtree.
- `# gazelle:license_subtree <dir> <label>` assigns a license to a directory and below from any BUILD file (usually the root), for when subtree BUILD files can't be edited, such as external repos. It beats `license_default` there.
- Every directory Gazelle visits is tagged; creating new BUILD files is governed by `build_file_generation` (go_repository).
- The `package_metadata` target named by the directive must exist; this extension does not create it.

## Usage

```starlark
load("@gazelle//:def.bzl", "gazelle", "gazelle_binary")

gazelle_binary(
    name = "gazelle_supply_chain_bin",
    languages = ["@gazelle_supply_chain//:gazelle_supply_chain"],
)

gazelle(name = "gazelle", gazelle = ":gazelle_supply_chain_bin")
```

Run `bazel run //:gazelle`. Re-running is idempotent. If other Gazelle languages
are enabled, add them to `languages` too.

The same settings are available as flags: `-license_default=<label>`, `-license_subtree=<dir>=<label>` (repeatable). Combine with `-lang=license` to only append tags to existing BUILD files.

## External repos (go_deps)

`go_repository` runs a fixed Gazelle binary, so it can't see this plugin. `tools_repo.bzl` builds a Gazelle that includes it, and `override_repo` swaps it in for `@bazel_gazelle_go_repository_tools`. For a repo that ships its own BUILD files, pass `-lang=license` and the flags above through `gazelle_override(build_extra_args = ...)` to append only the tag. See `examples/`.

## Development

`bazel test //...`
