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

### Generated license targets

The plugin never invents a package identity. Next to each scanned license file, in the directory where the declaration was made, it writes one `license()` named `gazelle_generated_license` (or `gazelle_generated_license_<file suffix>`) with `text` set to that file, plus a `license_kind()` for a `LicenseRef-` id.

Target names are always `gazelle_generated_` followed by a suffix:

- `license()`: `gazelle_generated_<suffix>`, where `<suffix>` is the license file name lowercased, with every character that isn't `a-z` or `0-9` replaced by `_`, and leading and trailing `_` trimmed. `LICENSE` gives `gazelle_generated_license`, `LICENSE.txt` gives `gazelle_generated_license_txt`, `LICENSE-APACHE` gives `gazelle_generated_license_apache`, and `COPYING` gives `gazelle_generated_copying`. If the directory has no matching file, a single `gazelle_generated_license` is written without `text`.
- `license_kind()` (only for `LicenseRef-` ids): `gazelle_generated_kind`.
- `package_metadata()` (only when created, see below): the name in the declared label, for example `package_metadata` in `//enterprise:package_metadata`.

The declared label must name a `package_metadata`. If it already exists (for example the `//_gazelle_generated:package_metadata` that `go_repository` writes before running Gazelle), the licenses are appended to its `attributes` and its `purl` is kept. If it doesn't exist and the declaration was made in that package, one is created with the purl of the repository's generated metadata plus `#<package>` as subpath (fallback `pkg:golang/<gazelle:prefix>`). Otherwise nothing is generated. The repo needs `@package_metadata` visible. Every package except `_gazelle_generated` gets `package(default_applicable_licenses = [label])`.

This requires a `go_repository` that writes its metadata before Gazelle runs.

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
