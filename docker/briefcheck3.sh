#!/bin/sh
curl -s "http://localhost:8334/assets/bundle.js?version=939e550::wdt&chunk=1" -o /tmp/c1
echo "--- full installHomeShortcut body ---"
# print the function body: from 'async function installHomeShortcut' 60 lines
awk '/async function installHomeShortcut/,/^}/' /tmp/c1 | head -60
echo DONE