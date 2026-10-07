#!/bin/sh
curl -s "http://localhost:8334/assets/bundle.js?version=939e550::wdt&chunk=1" -o /tmp/c1
echo "--- exact home link code ---"
grep -o 'const home = [^;]*;' /tmp/c1 | head -2
grep -o 'toHref(..files. + encodeURI Component[^)]*)' /tmp/c1 | head -2
grep -o 'toHref([^)]*)' /tmp/c1 | sort -u | head -6
echo "--- who calls installHomeShortcut ---"
grep -o 'installHomeShortcut([^)]*)' /tmp/c1 | sort -u
echo DONE