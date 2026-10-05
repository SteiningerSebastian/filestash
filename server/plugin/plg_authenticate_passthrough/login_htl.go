package plg_authenticate_passthrough

import "strings"

/*
 * HTL Neufelden login-page enhancements for the "username_and_password"
 * strategy (fork feature, not upstream):
 *
 *   1. LAST-USED HINT: the username is stored in a cookie (htl-user) after
 *      every successful login and prefilled on the next visit — an offer,
 *      never a lock-in: the field stays editable, so any account can be
 *      typed (all backends share one credential set per statement).
 *
 * The script is a single string, injected into the plain Page() HTML of the
 * passthrough middleware login page (server-rendered — no frontend build).
 */

func htlLoginScript() string {
	var s strings.Builder
	s.WriteString(`
(function() {
    "use strict";
    var COOKIE = "htl-user";
    var form = document.querySelector("form[action]");

    if (!form) return;
    var $user = form.querySelector('input[name="user"]');
    if (!$user) return;

    /* Storage is NOT guaranteed on this page: the login form is served from
     * /api/session/auth/ and strict browser settings (blocked third-party
     * cookies/partitioned storage, extensions, private mode, Firefox ETP)
     * make ANY access to document.cookie throw "Access to storage is not
     * allowed from this context". That must never break the plain FTP
     * login: every cookie touch is gated behind a probe and wrapped in
     * try/catch. The htl-user cookie is a hint, NEVER a credential. */
    function probe(fn) { try { return fn() === true; } catch (e) { return false; } }
    var cookieOK = probe(function() {
        document.cookie = "__htl_probe__=1; max-age=3600; path=/; SameSite=Lax; Secure";
        var ok = document.cookie.indexOf("__htl_probe__=") !== -1;
        document.cookie = "__htl_probe__=; max-age=0; path=/"; return ok;
    });
    function getCookie(name) {
        if (!cookieOK) return "";
        try {
            var m = document.cookie.match(new RegExp("(?:^|; )" + name + "=([^;]*)"));
            return m ? decodeURIComponent(m[1]) : "";
        } catch (e) { return ""; }
    }
    function setCookie(name, value, days) {
        if (!cookieOK) return;
        try {
            document.cookie = name + "=" + encodeURIComponent(value) +
                "; max-age=" + (days * 86400) + "; path=/; SameSite=Lax; Secure";
        } catch (e) {}
    }

    // Keep the vault out of every storage tier: entries written by the
    // retired remember-password feature are purged on first load (plain
    // storage access — the tier probes above only apply to new writes).
    function purgeLegacyVault() {
        var key = null;
        var last = getCookie(COOKIE);
        if (last) key = "htl-vault:" + last;
        try {
            for (var i = window.localStorage.length - 1; i >= 0; i--) {
                var k = window.localStorage.key(i);
                if (k && k.indexOf("htl-vault:") === 0) window.localStorage.removeItem(k);
            }
        } catch (e) {}
        try {
            for (var j = window.sessionStorage.length - 1; j >= 0; j--) {
                var k2 = window.sessionStorage.key(j);
                if (k2 && k2.indexOf("htl-vault:") === 0) window.sessionStorage.removeItem(k2);
            }
        } catch (e) {}
        if (key) {
            try { document.cookie = key + "=; max-age=0; path=/"; } catch (e) {}
        }
        try {
            if ("indexedDB" in window && indexedDB.deleteDatabase) {
                indexedDB.deleteDatabase("htl-vault-db");
            }
        } catch (e) {}
    }
    purgeLegacyVault();

    // 1 + submit: prefill last used; refresh the hint cookie on every login
    var last = getCookie(COOKIE);
    if (last && !$user.value) $user.value = last;

    form.addEventListener("submit", function() {
        if ($user.value) setCookie(COOKIE, $user.value, 365);
    });
})();
`)
	// all user-facing strings are plain ASCII/English; return as-is
	return s.String()
}
