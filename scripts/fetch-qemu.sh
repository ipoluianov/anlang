#!/bin/sh
# Downloads QEMU for x86 and ARM and SeaBIOS from Debian into external/qemu,
# without installing them. QEMU is used only by the tests of the
# toolchain, as an independent implementation (conf.oracle.use); it is
# not committed or distributed (spec/24-roadmap.md, step 12).
set -e
dir=$(cd "$(dirname "$0")/.." && pwd)/external/qemu
mkdir -p "$dir/debs"
pool=http://deb.debian.org/debian/pool/main
fetch() { # directory, package name pattern
	f=$(curl -sfL "$pool/$1/" | grep -o "$2" | sort -u | tail -1)
	[ -n "$f" ] || { echo "no $2 in $pool/$1" >&2; exit 1; }
	[ -s "$dir/debs/$f" ] || curl -sfL -o "$dir/debs/$f" "$pool/$1/$f"
	dpkg -x "$dir/debs/$f" "$dir/root"
}
fetch q/qemu 'qemu-system-x86_10\.0\.[0-9]*+ds-0+deb13u[0-9]*_amd64\.deb'
fetch q/qemu 'qemu-system-arm_10\.0\.[0-9]*+ds-0+deb13u[0-9]*_amd64\.deb'
fetch q/qemu 'qemu-system-common_10\.0\.[0-9]*+ds-0+deb13u[0-9]*_amd64\.deb'
fetch q/qemu 'qemu-system-data_10\.0\.[0-9]*+ds-0+deb13u[0-9]*_all\.deb'
fetch s/seabios 'seabios_1\.[0-9.]*-[0-9]*[^"]*_all\.deb'
"$dir/root/usr/bin/qemu-system-i386" -version
"$dir/root/usr/bin/qemu-system-arm" -version
