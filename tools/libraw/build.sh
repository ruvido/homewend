#!/bin/sh
# homewend — Copyright (C) 2026 ruvido
# SPDX-License-Identifier: AGPL-3.0-or-later

# Builds homewend-raw, linked with LibRaw, into electron/engine
# beside the engine, where electron-builder takes it into the app. LibRaw's
# own licence goes with it (LGPL-2.1, NOTICE).
#
# Usage: tools/libraw/build.sh
# The tarball is downloaded into $TMPDIR/homewend-libraw unless it is already
# there, and checked against its sha256 either way.
set -eu

version=0.21.5
url="https://www.libraw.org/data/LibRaw-$version.tar.gz"
sha256=a74a2e68303d3b9219f82318f935b28c5c4abd7f2c9f7dbf8faa4997c9038305

here="$(cd "$(dirname "$0")" && pwd)"
out="$here/../../electron/engine"
work="${TMPDIR:-/tmp}/homewend-libraw"
tarball="$work/LibRaw-$version.tar.gz"

mkdir -p "$work" "$out"
[ -f "$tarball" ] || curl -fsSL -o "$tarball" "$url"
echo "$sha256  $tarball" | sha256sum -c -
rm -rf "$work/LibRaw-$version"
tar -xzf "$tarball" -C "$work"
src="$work/LibRaw-$version"

# OpenMP spreads the demosaic over the cores, when the compiler has its
# static library (Void: libgomp-devel).
omp=
gomp="$(g++ -print-file-name=libgomp.a)"
if [ -f "$gomp" ] && echo '#include <omp.h>' | g++ -fopenmp -E -x c++ - >/dev/null 2>&1; then
	omp=-fopenmp
else
	gomp=
fi
echo "OpenMP: ${omp:-no}"

# shortcut: no zlib, JPEG or Jasper, so deflate (floating-point) DNG, lossy
# DNG and RED files are not read; add them when someone has such files.
flags="-O3 -I. -w $omp"
make -C "$src" -f Makefile.dist -j"$(nproc)" lib/libraw.a CFLAGS="$flags" LDADD=
# LibRaw, libgomp and the C++ runtime inside, the C library the system's:
# linked dynamically, as the LGPL asks of a library the user may replace.
g++ -DLIBRAW_NOTHREADS -O3 -w -I"$src" -static-libstdc++ -static-libgcc -s -o "$out/homewend-raw" \
	"$here/homewend-raw.cpp" "$src/lib/libraw.a" $gomp ${gomp:+-lpthread} -lm
cp "$src/LICENSE.LGPL" "$out/LibRaw.LICENSE.LGPL"
cp "$src/COPYRIGHT" "$out/LibRaw.COPYRIGHT"
# LibRaw's source, as built, goes with the release (NOTICE).
cp "$tarball" "$out/"
