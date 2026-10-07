#!/bin/sh
# checks the LINK construction inside served chunk1
curl -s "http://localhost:8334/assets/bundle.js?version=939e550::wdt&chunk=1" -o /tmp/c1
echo "--- link construction lines ---"
grep -o 'Users... + username' /tmp/c1 | head -3
grep -o '"/Users/"' /tmp/c1 | wc -l
grep -o 'files. + encodeURIComponent(home)' /tmp/c1 | head -2
echo "--- root folder injection in chunk4 ---"
curl -s "http://localhost:8334/assets/bundle.js?version=939e550::wdt&chunk=4" -o /tmp/c4
grep -o 'htlHomeUser(home)' /tmp/c4 | head -2
grep -o 'name: username, type: .directory.' /tmp/c4 | head -2
grep -o 'name: .Users., type' /tmp/c4 | head -2
echo DONE