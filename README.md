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
- A target can be followed by the SPDX id it stands for: `# gazelle:license_default //:mit MIT`. Strict mode needs it.
- Every directory Gazelle visits is tagged; creating new BUILD files is governed by `build_file_generation` (go_repository).
- The `package_metadata` target named by the directive must exist; this extension does not create it.

## Strict mode

```starlark
# gazelle:license_strict true
```

Gazelle then reads every license file it visits (see [Which files are scanned](#which-files-are-scanned)), matches it against known license texts with the configured detector (see [Detectors and confidence](#detectors-and-confidence)), and fails when:

- the file doesn't match a standard license (below the detector's confidence), including files that mix several licenses;
- nothing declares its license: the repo root needs `license_default`, and a subdirectory needs a `license_subtree` covering it or a `license_default` in its own BUILD file (a default inherited from a parent does not count);
- the declaration has no SPDX id, or the id isn't among the licenses found in the file.

A declaration whose id starts with `LicenseRef-` names a custom license (for example `-license_subtree="enterprise=//ent:meta LicenseRef-Acme"`). Its license files have no SPDX text to match, so they are skipped, as long as the declaration is explicit.

### Which files are scanned

By default: `LICENSE`, `LICENCE`, `COPYING` and `UNLICENSE`, plus `.txt`, `.md` and `.rst` variants where they exist, case-insensitively. `NOTICE` and `PATENTS` are not scanned: they are attribution and patent grants, not license texts.

```starlark
# gazelle:license_files default NOTICE LICENSE-*
```

`license_files` replaces the list for the directory and below. Names are case-insensitive globs, separated by spaces or commas, and `default` stands for the standard list. As a flag: `-license_files="default NOTICE"`.

### Detectors and confidence

Two detectors are available and you pick one:

| Detector | How it decides | Default confidence |
|---|---|---|
| `licensecheck` (default) | share of the file's words covered by SPDX license texts | 90 |
| `licenseclassifier` | match confidence; the matches must also cover that share of the file's lines | 80 (also the minimum) |

```starlark
# gazelle:license_detector licenseclassifier
# gazelle:license_confidence licensecheck 95
# gazelle:license_confidence licenseclassifier 85
```

Both directives take an optional trailing file, relative to the BUILD file, to apply to that one file only:

```starlark
# gazelle:license_detector licenseclassifier enterprise/LICENSE
# gazelle:license_confidence licensecheck 65 LICENSE
```

A per-file setting wins over the global one. The confidence used for a file is the one set for the detector that ends up reading it. As flags, quote the pair: `-license_confidence="licensecheck=65 LICENSE"`, `-license_detector="licenseclassifier LICENSE"` (paths relative to the repo root).

Confidence is set per detector, so switching detectors keeps each one's own value. Flags: `-license_detector=<name>` and `-license_confidence=<detector>=<percent>` (repeatable). `licenseclassifier` can't go below 80 because its embedded corpus only reports matches at or above that.

See [examples/](examples/) for a real-world project using strict mode, per-file confidence and `LicenseRef-` declarations.

There is no override here. A declaration must match what the project ships; correcting it is a concluded license, to be handled by `supply_chain_tools` (not defined yet).

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

The same settings are available as flags: `-license_default=<label>`, `-license_subtree=<dir>=<label>` (repeatable) and `-license_strict`; a label may be followed by its SPDX id, e.g. `-license_default="//:mit MIT"`. Combine with `-lang=license` to only append tags to existing BUILD files.

## External repos (go_deps)

`go_repository` runs a fixed Gazelle binary, so it can't see this plugin. `tools_repo.bzl` builds a Gazelle that includes it, and `override_repo` swaps it in for `@bazel_gazelle_go_repository_tools`. For a repo that ships its own BUILD files, pass `-lang=license` and the flags above through `gazelle_override(build_extra_args = ...)` to append only the tag. See `examples/`.

## Development

`bazel test //...`
