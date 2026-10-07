#!/bin/sh
base="http://localhost:8334"
curl -s "$base/" -o /tmp/idx.html
n=$(grep -o "chunk=[0-9]*" /tmp/idx.html | sort -u | tail -1 | sed "s/chunk=//")
echo "chunks_total=$n"
i=1
while [ "$i" -le "$n" ]; do
  curl -s "$base/assets/bundle.js?version=939e550::wdt&chunk=$i" -o /tmp/chunk.js
  size=$(wc -c < /tmp/chunk.js)
  a=$(grep -c installHomeShortcut /tmp/chunk.js || echo 0)
  b=$(grep -c htl-quickshare /tmp/chunk.js || echo 0)
  c=$(grep -c htlFilterDirectory /tmp/chunk.js || echo 0)
  echo "chunk$i size=$size home=$a quickshare=$b filterdir=$c"
  i=$((i+1))
done
echo DONE
