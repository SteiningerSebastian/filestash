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

    function getCookie(name) {
        var m = document.cookie.match(new RegExp("(?:^|; )" + name + "=([^;]*)"));
        return m ? decodeURIComponent(m[1]) : "";
    }
    function setCookie(name, value, days) {
        document.cookie = name + "=" + encodeURIComponent(value) +
            "; max-age=" + (days * 86400) + "; path=/; SameSite=Lax; Secure";
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
    if (supportsPRF && PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable) {
        PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable()
            .then(function(ok) { supportsPRF = ok; autoSignOn(); })
            .catch(function() { autoSignOn(); });
    } else {
        autoSignOn();
    }

    // 1 + 2: prefill last used; offer auto-sign-on when a vault entry exists
    function autoSignOn() {
        var last = getCookie(COOKIE);
        if (last && !$user.value) {
            $user.value = last;
        }
        var entry = last && localStorage.getItem(vaultKey(last));
        if (!entry) return;

        var blob = JSON.parse(entry);
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
    }

    // remember-on-success banner (shown after a manual login via ?saved=1)
    var url = new URL(location.href);
    if (url.searchParams.get("htl") === "saved") return;
    if (url.searchParams.get("htl") === "remember" && $user.value) {
        offerRemember($user.value, $pass.value);
    }

    // 3: on successful manual login, submit sets the cookie BEFORE nav
    form.addEventListener("submit", function() {
        if ($user.value) setCookie(COOKIE, $user.value, 365);
    });
    // ...then the server redirects back with __next; the remember-flow is
    // triggered via the query param the callback URL keeps (htl=remember)
    function offerRemember(username, password) {
        if (!supportsPRF || !password) return;
        var banner = document.createElement("div");
        banner.className = "htl-remember";
        banner.style.cssText = "max-width:450px;margin:12px auto 0;padding:12px 16px;" +
            "background:#fff;border:1px solid rgba(73,73,73,0.15);font-size:0.92em;" +
            "display:flex;align-items:center;justify-content:space-between;gap:12px;";
        banner.innerHTML = '<span>Kennstoff u00fcr <strong>' + username +
            '</strong> auf diesem Gere00e4t merken?</span>';
        var yes = document.createElement("button");
        yes.type = "button";
        yes.textContent = "Merken";
        yes.style.cssText = "padding:8px 18px;background:#009883;color:#fff;" +
            "border:none;cursor:pointer;font-weight:600;text-transform:uppercase;" +
            "font-size:0.85em;letter-spacing:0.04em;";
        var no = document.createElement("button");
        no.type = "button";
        no.textContent = "Nein";
        no.style.cssText = "padding:8px 14px;background:#fff;color:#494949;" +
            "border:1px solid rgba(73,73,73,0.25);cursor:pointer;margin-left:8px;";
        banner.appendChild(yes); banner.appendChild(no);
        form.parentElement.insertBefore(banner, form.nextSibling);

        yes.addEventListener("click", function() {
            // encrypt: register a PRF credential only if none yet, else reuse
            // (allowCredentials unknown here; get() with empty allow list +
            // prf ext gives the platform credential's PRF secret)
            var allow = null;
            var entry = localStorage.getItem(vaultKey(username));
            if (entry) { try { allow = JSON.parse(entry).allowCredentials; } catch (e) {} }
            var credPromise = allow
                ? Promise.resolve(allow)
                : navigator.credentials.get({
                    publicKey: {
                        challenge: crypto.getRandomValues(new Uint8Array(32)),
                        rpId: location.hostname,
                        userVerification: "preferred",
                        extensions: { prf: { eval: { first: prfSalt() } } }
                    }
                }).then(function(cred) {
                    return [{
                        type: cred.type,
                        id: b64u(cred.rawId)
                    }];
                });
            credPromise
                .then(function(allowList) {
                    return getPrf(allowList).then(keyFromPrf).then(function(key) {
                        var iv = crypto.getRandomValues(new Uint8Array(12));
                        return crypto.subtle.encrypt(
                            { name: "AES-GCM", iv: iv },
                            key, enc(password)
                        ).then(function(cipher) {
                            localStorage.setItem(vaultKey(username), JSON.stringify({
                                iv: b64u(iv.buffer),
                                data: b64u(cipher),
                                allowCredentials: allowList
                            }));
                            setCookie(COOKIE, username, 365);
                            banner.remove();
                            flash("Gemesichert u2013 beim ne00e4chsten Mal wirst du automatisch angemeldet.");
                        });
                    });
                })
                .catch(function() { flash("Konnte nicht gespeichert werden (Authenticator ohne PRF?)"); });
        });
        no.addEventListener("click", function() { banner.remove(); });
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