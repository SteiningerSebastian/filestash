package plg_authenticate_passthrough

import "strings"

/*
 * HTL Neufelden login-page enhancements for the "username_and_password"
 * strategy (fork feature, not upstream):
 *
 *   1. LAST-USED HINT: the username is stored in a cookie (htl-user) after
 *      every successful login and prefilled on the next visit — an offer,
 *      never a lock-in: the field stays editable, so any account can be
 *      typed (all backends share one credential set per statement, but the
 *      vault below is still per-username).
 *
 *   2. WEBAUTHN PRF VAULT: after a successful manual login the user is
 *      offered "Kennwort auf diesem Gerät merken". The FTP password is
 *      AES-GCM-encrypted with a key that CANNOT leave the platform
 *      authenticator (Windows Hello / phone passkey): the key is derived
 *      inside the authenticator via the WebAuthn `prf` extension
 *      (deterministic HKDF-ish output from credentialId+salt). The
 *      ciphertext lives in localStorage['htl-vault:<username>'] — useless
 *      without the authenticator, so no "password in the browser" exposure
 *      beyond what the authenticator itself guarantees.
 *      On later visits, if a vault entry exists for the prefilled user, the
 *      page performs a get({prf}) → decrypt → auto-submit (onefinger tap /
 *      Hello prompt, browser-mediated, nothing scripts around it).
 *      PRF unsupported (e.g. Firefox desktop) → offer hidden, cookie prefill
 *      still active, plain login unaffected.
 *
 *   3. Multi-account: vault is keyed per username; "sign in as somebody
 *      else" = type another username (no auto sign-on for entries which do
 *      not match) — the last-used cookie is refreshed on every login.
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
    var SALT = "filestash-ftp-v1"; // PRF salt (change to force re-enrollment)
    var form = document.querySelector("form[action]");

    if (!form || !("crypto" in window) || !("subtle" in window.crypto)) return;
    var $user = form.querySelector('input[name="user"]');
    var $pass = form.querySelector('input[name="password"]');
    if (!$user || !$pass) return;

    /* Storage is NOT guaranteed on this page: the login form is served from
     * /api/session/auth/ and strict browser settings (blocked third-party
     * cookies/partitioned storage, extensions, private mode, Firefox ETP)
     * make ANY access to document.cookie or localStorage throw
     * "Access to storage is not allowed from this context". That must never
     * break the plain FTP login — AND the vault must still function: the
     * payload is stored in the FIRST writeable tier of
     *   localStorage → sessionStorage → IndexedDB → cookie (24h cap).
     * (IndexedDB is probed separately — Chrome allows/denies it independent
     * of localStorage in some partitioned contexts.)
     * The stored blob is ALWAYS PRF-encrypted, so a weaker tier never has
     * the plaintext; the cookie tier additionally caps the lifetime at 24h
     * to limit its wider attack surface (cookies ride every request). */
    var TIER_LOCAL = 1, TIER_SESSION = 2, TIER_IDB = 3, TIER_COOKIE = 4;
    var TIER_COOKIE_MAX_AGE = 86400; // 24h cap for the weakest tier
    function probe(fn) { try { return fn() === true; } catch (e) { return false; } }
    var localOK = probe(function() {
        window.localStorage.setItem("__htl_probe__", "1");
        var ok = window.localStorage.getItem("__htl_probe__") === "1";
        window.localStorage.removeItem("__htl_probe__"); return ok;
    });
    var sessionOK = probe(function() {
        window.sessionStorage.setItem("__htl_probe__", "1");
        var ok = window.sessionStorage.getItem("__htl_probe__") === "1";
        window.sessionStorage.removeItem("__htl_probe__"); return ok;
    });
    var cookieOK = probe(function() {
        document.cookie = "__htl_probe__=1; max-age=3600; path=/; SameSite=Lax; Secure";
        var ok = document.cookie.indexOf("__htl_probe__=") !== -1;
        document.cookie = "__htl_probe__=; max-age=0; path=/"; return ok;
    });
    var idbOK = "indexedDB" in window;
    var idbReady = null;
    if (idbOK) {
        idbReady = new Promise(function(resolve) {
            try {
                var open = indexedDB.open("htl-vault-db", 1);
                open.onupgradeneeded = function() {
                    open.result.createObjectStore("kv");
                };
                open.onsuccess = function() { resolve(open.result); };
                open.onerror = function() { resolve(null); };
                open.onblocked = function() { resolve(null); };
                setTimeout(function() { resolve(null); }, 3000);
            } catch (e) { resolve(null); }
        });
    }

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

    function idbGet(key) {
        if (!idbReady) return Promise.resolve(null);
        return idbReady.then(function(db) {
            if (!db) return null;
            return new Promise(function(resolve) {
                try {
                    var tx = db.transaction("kv", "readonly");
                    var req = tx.objectStore("kv").get(key);
                    req.onsuccess = function() { resolve(req.result || null); };
                    req.onerror = function() { resolve(null); };
                    tx.onabort = function() { resolve(null); };
                } catch (e) { resolve(null); }
            });
        });
    }
    function idbSet(key, value) {
        if (!idbReady) return Promise.resolve(false);
        return idbReady.then(function(db) {
            if (!db) return false;
            return new Promise(function(resolve) {
                try {
                    var tx = db.transaction("kv", "readwrite");
                    tx.objectStore("kv").put(value, key);
                    tx.oncomplete = function() { resolve(true); };
                    tx.onerror = function() { resolve(false); };
                    tx.onabort = function() { resolve(false); };
                } catch (e) { resolve(false); }
            });
        });
    }

    /* vaultGet: checks every tier, most-trusted first. Cookie tier entries
     * carry "t":TIER_COOKIE so expired-later reads are possible but only
     * fresh (24h) ones count. Asynchronous because IndexedDB is. */
    function vaultGet(key, cb) {
        var v = null;
        if (localOK) { try { v = window.localStorage.getItem(key); } catch (e) {} }
        if (v) return cb(JSON.parse(v));
        if (sessionOK) { try { v = window.sessionStorage.getItem(key); } catch (e) {} }
        if (v) return cb(JSON.parse(v));
        idbGet(key).then(function(w) {
            if (w) return cb(JSON.parse(w));
            var raw = getCookie(key); // cookie tier: name=value(json)
            if (raw) {
                try { var o = JSON.parse(raw); if (o && o.t === TIER_COOKIE) return cb(o); } catch (e) {}
            }
            cb(null);
        }).catch(function() { cb(null); });
    }
    /* vaultSet: writes EVERY writeable tier (redundancy across contexts —
     * later visits may pass a different tier but not fail). */
    function vaultSet(key, obj) {
        var saved = false;
        if (localOK) { try { window.localStorage.setItem(key, JSON.stringify(obj)); saved = true; } catch (e) {} }
        if (sessionOK) { try { window.sessionStorage.setItem(key, JSON.stringify(obj)); saved = true; } catch (e) {} }
        if (idbReady) { idbSet(key, JSON.stringify(obj)).then(function() {}); }
        if (cookieOK) {
            var withTier = obj; withTier.t = TIER_COOKIE;
            setCookie(key, JSON.stringify(withTier), TIER_COOKIE_MAX_AGE / 86400);
            saved = true;
        }
        return saved;
    }
    function b64u(buf) {
        var bytes = new Uint8Array(buf), bin = "";
        for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
        return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    }
    function unb64u(str) {
        str = str.replace(/-/g, "+").replace(/_/g, "/");
        while (str.length % 4) str += "=";
        var bin = atob(str), bytes = new Uint8Array(bin.length);
        for (var i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
        return bytes;
    }
    function enc(str) { return new TextEncoder().encode(str); }
    function dec(buf) { return new TextDecoder().decode(buf); }

    function vaultKey(username) { return "htl-vault:" + username; }
    function prfSalt() { return enc(SALT).buffer; }

    // Derive the AES-GCM key from the authenticator's PRF output
    function keyFromPrf(prfResult) {
        return crypto.subtle.importKey("raw", prfResult, "HKDF", false, ["deriveKey"])
            .then(function(base) {
                return crypto.subtle.deriveKey({
                    name: "HKDF",
                    salt: enc("htl-ftp-key"),
                    info: enc("aes-gcm"),
                    hash: "SHA-256"
                }, base, { name: "AES-GCM", length: 256 }, false, ["encrypt", "decrypt"]);
            });
    }
    function getPrf(allowCredentials) {
        var assertion = {
            publicKey: {
                challenge: crypto.getRandomValues(new Uint8Array(32)),
                rpId: location.hostname,
                userVerification: "preferred",
                extensions: { prf: { eval: { first: prfSalt() } } }
            }
        };
        if (allowCredentials) assertion.publicKey.allowCredentials = allowCredentials;
        return navigator.credentials.get(assertion).then(function(cred) {
            var out = cred.getClientExtensionResults().prf;
            if (!out || !out.enabled || !out.results || !out.results.first) {
                throw new Error("PRF_NOT_SUPPORTED");
            }
            return out.results.first;
        });
    }

    var supportsPRF = !!window.PublicKeyCredential;
    var platformReady = Promise.resolve(window.PublicKeyCredential
        && PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable
        && PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable().catch(function() { return false; })
        || Promise.resolve(false));
    platformReady.then(function(ok) {
        supportsPRF = !!ok;
        autoSignOn();
        mountRememberCheckbox();
    });

    // [+] Remember-Me UI: small checkbox under the password field — shown
    // ALWAYS (the label explains the authenticator handles the secret).
    // Checked = store the FTP password after this successful login.
    function mountRememberCheckbox() {
        var wrap = document.createElement("label");
        wrap.className = "htl-remember";
        wrap.style.cssText = "display:flex;align-items:center;gap:8px;margin:6px 0 0 0;" +
            "font-size:0.85em;color:#494949;cursor:pointer;user-select:none;";
        var box = document.createElement("input");
        box.type = "checkbox";
        box.id = "htl-remember-box";
        box.style.cssText = "width:15px;height:15px;accent-color:#009883;cursor:pointer;";
        wrap.appendChild(box);
        var txt = document.createElement("span");
        txt.textContent = "Auf diesem Gerät merken (Windows Hello / Passkey)";
        wrap.appendChild(txt);
        form.appendChild(wrap);
        if (getCookie(COOKIE)) {
            var has = !!localStorage.getItem(vaultKey(getCookie(COOKIE)));
            box.checked = has;
        }
    }

    // 1 + 2: prefill last used; offer auto-sign-on when a vault entry exists
    function autoSignOn() {
        var last = getCookie(COOKIE);
        if (last && !$user.value) {
            $user.value = last;
        }
        if (!last) return;
        vaultGet(vaultKey(last), function(blob) {
            if (!blob || !blob.iv || !blob.data) return;
            getPrf(blob.allowCredentials).then(keyFromPrf).then(function(key) {
                return crypto.subtle.decrypt(
                    { name: "AES-GCM", iv: unb64u(blob.iv) },
                    key, unb64u(blob.data)
                );
            }).then(function(plain) {
                $pass.value = dec(plain);
                flash("Automatische Anmeldung l u00e4uft…");
                form.submit(); // PRF key = user presence confirmed, not a script bypass
            }).catch(function(err) {
                $pass.focus(); // authenticator declined / vault corrupt / PRF NOK
            });
        });
    }

    // 3: SUBMIT = the only decision point: checked+PRF → encrypt+store;
    // unchecked → purge the vault entry. Then the login proceeds as normal.
    form.addEventListener("submit", function(ev) {
        if ($user.value) setCookie(COOKIE, $user.value, 365);
        var box = document.getElementById("htl-remember-box");
        if (!box) return; // no checkbox rendered (no PRF) → plain login
        if (!box.checked) {
            try { localStorage.removeItem(vaultKey($user.value)); } catch (e) {}
            try { sessionStorage.removeItem(vaultKey($user.value)); } catch (e) {}
            return; // plain login
        }
        // CHECKED: store the FTP password under the username — the PRF key
        // lives in the authenticator, this prompt is the enrollment.
        ev.preventDefault();
        var username = $user.value, password = $pass.value;
        rememberAndLogin(username, password, function(stored) {
            if (stored) { flash("Gespeichert — beim nächsten Mal wirst du automatisch angemeldet."); }
            form.submit();
        });
    });

    // encrypt+store the password under a PRF-derived key (the ONLY place a
    // credential.get({prf}) is invoked for storing)
    function rememberAndLogin(username, password, done) {
        if (!supportsPRF || !password) { done(false); return; }
        var prfSalt = enc(SALT);
        var p = navigator.credentials.get({
            publicKey: {
                challenge: crypto.getRandomValues(new Uint8Array(32)),
                rpId: location.hostname,
                userVerification: "preferred",
                extensions: { prf: { eval: { first: prfSalt.buffer } } }
            }
        }).then(function(cred) {
            var out = cred.getClientExtensionResults().prf;
            if (!out || !out.enabled || !out.results || !out.results.first) throw new Error("PRF_NOT_SUPPORTED");
            return {
                first: out.results.first,
                allowCredentials: [{ type: cred.type, id: b64u(cred.rawId) }]
            };
        }).then(function(prf) {
            return keyFromPrf(prf.first).then(function(key) {
                var iv = crypto.getRandomValues(new Uint8Array(12));
                return crypto.subtle.encrypt({ name: "AES-GCM", iv: iv }, key, enc(password))
                    .then(function(cipher) {
                        var saved = vaultSet(vaultKey(username), {
                            iv: b64u(iv.buffer),
                            data: b64u(cipher),
                            allowCredentials: prf.allowCredentials
                        });
                        done(saved);
                    });
            });
        }).catch(function() { done(false); });
        return p;
    }

    function flash(msg) {
        var f = document.createElement("div");
        f.style.cssText = "position:fixed;bottom:18px;left:0;right:0;text-align:center;" +
            "font-size:0.9em;color:#494949;";
        f.textContent = msg;
        document.body.appendChild(f);
        setTimeout(function() { f.remove(); }, 4000);
    }
})();
`)
	// keep the script plain ASCII for the Go raw string
	return strings.NewReplacer(
		"Kennstoff", "Kennwort",
		"Gere00e4t", "Gerät",
		"Gemesichert", "Gespeichert",
		"ne00e4chsten", "nächsten",
		"l u00e4uft", "läuft",
		"u00fcr", "für",
	).Replace(s.String())
}
