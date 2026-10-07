/*
 * Offline test harness for the HTL fork helpers (public/assets/pages/
 * filespage/helper.js). Run:  node helper.test.js
 * The helper module imports DOM-touching deps (assert -> document), so the
 * three pure functions are duplicated here verbatim; the test asserts the
 * EXACT contract used by ctrl_homepage.js / sidebar_files.js.
 */

// ---- copies of helper.js exports (keep in sync!) ------------------------
const HTL_SYSTEM_SHARES = ["fileexchange", "dfs", "users"];

const htlHomeUser = (path) => {
    const chunks = (path || "").split("/").filter((chunks) => chunks !== "");
    if (chunks.length < 2 || chunks[0].toLowerCase() !== "users") return "";
    return decodeURIComponent(chunks[1]);
};

const htlHomeDir = (user) => user ? "/Users/" + user + "/" : "";

const htlIsHiddenShare = (name = "") =>
    HTL_SYSTEM_SHARES.indexOf(name.toLowerCase()) !== -1;

const htlFilterDirectory = (parentPath, file) =>
    parentPath === "/" ? htlIsHiddenShare(file.name) === false : true;
// -------------------------------------------------------------------------

import assert from "node:assert";

// htlHomeUser: the homepage receives session home; sidebar receives paths
assert.strictEqual(htlHomeUser("/Users/atn20172033/"), "atn20172033"); // landed after redirect
assert.strictEqual(htlHomeUser("/Users/atn20172033/school/"), "atn20172033"); // browsing deeper
assert.strictEqual(htlHomeUser("/users/A2024B/"), "A2024B"); // case-insensitive share name
assert.strictEqual(htlHomeUser("/Users/f1le%20exch/"), "f1le exch"); // URL-encoded username decoded
assert.strictEqual(htlHomeUser("/"), ""); // storage root: no shortcut
assert.strictEqual(htlHomeUser("/Users/"), ""); // /Users itself: no shortcut (no name yet)
assert.strictEqual(htlHomeUser("/FileExchange/"), ""); // not a /Users path
assert.strictEqual(htlHomeUser("/DFS/Deep/Path/"), ""); // DFS subtree: nothing to offer
assert.strictEqual(htlHomeUser(""), "");
assert.strictEqual(htlHomeUser(null), "");

// htlHomeDir
assert.strictEqual(htlHomeDir("atn20172033"), "/Users/atn20172033/");
assert.strictEqual(htlHomeDir(""), "");

// htlIsHiddenShare: name matching is case-insensitive on the share names
assert.strictEqual(htlIsHiddenShare("fileexchange"), true);
assert.strictEqual(htlIsHiddenShare("FileExchange"), true);
assert.strictEqual(htlIsHiddenShare("dfs"), true);
assert.strictEqual(htlIsHiddenShare("users"), true);
assert.strictEqual(htlIsHiddenShare("Users"), true);
assert.strictEqual(htlIsHiddenShare("atn20172033"), false); // user folders stay visible
assert.strictEqual(htlIsHiddenShare("12a_atn"), false);
assert.strictEqual(htlIsHiddenShare("classrooms"), false);
assert.strictEqual(htlIsHiddenShare(""), false);
assert.strictEqual(htlIsHiddenShare(), false); // default param (search results w/o name)

// ---- htlFilterDirectory: TOP LEVEL ONLY hiding -------------------------
// storage root: the 3 system shares vanish, real content stays
assert.deepStrictEqual(
    ["FileExchange", "dfs", "Users", "A2024B", "Classrooms", "readme.txt"]
        .map((name) => ({ name }))
        .filter((f) => htlFilterDirectory("/", f))
        .map((f) => f.name),
    ["A2024B", "Classrooms", "readme.txt"],
);
// one level deeper: a custom "Users" folder MUST stay visible (user request)
assert.deepStrictEqual(
    ["Users", "2024-25", "notes.txt"]
        .map((name) => ({ name }))
        .filter((f) => htlFilterDirectory("/Classrooms/", f))
        .map((f) => f.name),
    ["Users", "2024-25", "notes.txt"],
);
assert.deepStrictEqual(
    ["dfs", "other"]
        .map((name) => ({ name }))
        .filter((f) => htlFilterDirectory("/Users/jdoe/", f))
        .map((f) => f.name),
    ["dfs", "other"],
);

// ---- ctrl_filesystem.js filter semantics (root list only) ---------------
const rootFiles = [
    { name: "FileExchange", type: "directory" },
    { name: "dfs", type: "directory" },
    { name: "Users", type: "directory" },
    { name: "A2024B", type: "directory" },
    { name: "Classrooms", type: "directory" },
    { name: "readme.txt", type: "file" },
];
const visible = rootFiles
    .filter((f) => htlIsHiddenShare(f.name) === false);
assert.deepStrictEqual(
    visible.map((f) => f.name),
    ["A2024B", "Classrooms", "readme.txt"],
);

// search results carry path instead of name -> basename fallback must work
const searchRes = [{ path: "/Users/x/fileexchange/foo.txt", type: "file" }];
const nameFallback = basename(searchRes[0].path);
function basename(str, sep = "/") {
    return str.substr(str.lastIndexOf(sep) + 1);
}
assert.strictEqual(htlIsHiddenShare(nameFallback), false); // searched files never filtered by share name

// ---- homepage redirect decisions ---------------------------------------
const nav = (sessionHome) => {
    const homeUser = htlHomeUser(sessionHome);
    return homeUser ? htlHomeDir(homeUser) : sessionHome || "/";
};
assert.strictEqual(nav("/"), "/"); // GetHome reported root (eg. user IS /Users/x -> home becomes /Users/x, but root session falls back)
assert.strictEqual(nav("/Users/atn20172033/"), "/Users/atn20172033/");
assert.strictEqual(nav("/"), "/");
assert.strictEqual(nav("/anything/else/"), "/anything/else/");

console.log("ALL HTL HELPER CHECKS PASSED");