# Contributing to smon

Thanks for your interest in smon! Bug reports, fixes, new compositor backends, receiver
support and documentation improvements are all welcome.

## Reporting bugs

Include:

- your compositor and its version (`hyprctl version`, or your umbriel version),
- your distribution, and the GStreamer version (`gst-inspect-1.0 --version`),
- the full command you ran and smon's log output,
- the receiver (browser and device, TV model, Miracast sink, …).

## Development setup

You need the build and runtime dependencies from [docs/installation.md](docs/installation.md).

```sh
make            # fmt-check, vet, lint, short tests, then build
make build      # build bin/smon only
make test       # full tests, including the real-encoder ones (needs GStreamer)
make test-short # skip the tests that run real encoders for several seconds
make fmt        # goimports + gofumpt
make lint       # staticcheck with every check enabled
make run ARGS="web -input"
```

The formatting and lint tools are pinned in the `Makefile` and run through `go run`, so you
don't need to install them.

## Pull requests

- Run `make` before pushing. It must pass cleanly.
- Keep changes focused: one feature or fix per pull request.
- Add tests for logic that can run without a compositor (parsing, negotiation, placement).
  See the existing `*_test.go` files.
- Update the docs in `docs/` and the README when you change behavior, flags or requirements.

## Code guidelines

- smon must leave the system as it found it. Compositor and audio changes are runtime-only and
  undone on exit, and user config files are never edited.
- Comments explain *why*. Exported identifiers have doc comments.

## Adding a compositor

This is the most wanted contribution. See
[Adding a compositor](docs/architecture.md#adding-a-compositor).

## License

By contributing, you agree that your contributions are licensed under the
[GPL-3.0-or-later](LICENSE), like the rest of smon.
