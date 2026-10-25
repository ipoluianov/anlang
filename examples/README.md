# Kern examples

Each example is a separate project: a directory with its own `kern.mod`.

| Example | Profile | Targets | Shows |
| --- | --- | --- | --- |
| [hello](hello/) | `app` | `linux-x86_64`, `macos-arm64` | The smallest program: printing a line |
| [hello-dos](hello-dos/) | `core` | `dos-com` | A DOS .COM program: compile-time CP866 encoding, `$`-terminated strings |
| [packages](packages/) | `app` | `linux-x86_64`, `macos-arm64` | Nested project packages (`myprogram/mydictionary`), an `internal` package, exported and unexported names, `inout` receivers, comma-ok lookup, package errors as an `enum` with `errors.Is` and `errors.As` |
| [bootsector](bootsector/) | `core` | `bios-boot` | A 512-byte BIOS boot sector: BIOS calls through `kern/arch/i8086`, the `_i8086` file suffix, a contract that removes overflow checks |

Build and run an example from its directory:

```
kern run                    # first target from kern.mod
kern run -emu dos-com       # hello-dos in the built-in 8086 interpreter
```

The boot sector is a raw 512-byte disk image in `out/bios-boot/`. Run it in QEMU or write it to the first sector of a disk:

```
qemu-system-i386 -drive format=raw,file=out/bios-boot/bootsector.bin
```
