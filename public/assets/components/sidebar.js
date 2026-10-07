import { createElement, createRender, onDestroy } from "../lib/skeleton/index.js";
import { navigate, toHref } from "../lib/skeleton/router.js";
import rxjs, { effect, onClick, preventDefault } from "../lib/rx.js";
import { qs, safe } from "../lib/dom.js";
import { settingsGet, settingsSave } from "../lib/store.js";
import { loadCSS } from "../helpers/loader.js";
import { getCurrentPath } from "../pages/viewerpage/common.js";
import { generateSkeleton } from "./skeleton.js";
import { getSession } from "../model/session.js";
import { htlHomeUser } from "../pages/filespage/helper.js";

import ctrlNavigationPane from "./sidebar_files.js";
import ctrlTagPane from "./sidebar_tags.js";

// HTL (fork): teal home-folder icon for the hardcoded home shortcut
const HTL_HOME_ICON = "data:image/svg+xml,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20viewBox='0%200%2016%2016'%3E%3Cpath%20fill='%23009883'%20d='M1.5%202A1.5%201.5%200%200%200%200%203.5V12.5A1.5%201.5%200%200%200%201.5%2014H14.5A1.5%201.5%200%200%200%2016%2012.5V5.5A1.5%201.5%200%200%200%2014.5%204H8.2L6.9%202.4A1%201%200%200%200%206.2%202Z'/%3E%3C/svg%3E";

// HTL (fork): school logo, hardcoded as inline HTML (user request).
// The same logo as cloud.apps.htl-neufelden.at; clicking it navigates to
// the storage root ("/files/") via the SPA router (like upstream "home").
const HTL_LOGO_HTML = `
    <a class="htl-brand" data-link href="${toHref("/files/")}" title="Home">
        <svg class="htl-brand-logo" viewBox="0 0 32 32" aria-hidden="true" focusable="false">
            <rect x="13" y="2" width="6" height="16" rx="1" fill="#009883"></rect>
            <rect x="5" y="8" width="6" height="10" rx="1" fill="#65d1c7"></rect>
            <rect x="21" y="8" width="6" height="10" rx="1" fill="#65d1c7"></rect>
            <rect x="2" y="20" width="28" height="4" rx="1" fill="#009883"></rect>
            <rect x="14" y="26" width="4" height="5" rx="1" fill="#494949"></rect>
        </svg>
        <span class="htl-brand-name">HTL Neufelden</span>
    </a>
`;

export default async function ctrlSidebar(render, {}) {
    if (new URL(location.toString()).searchParams.get("nav") === "false") return;
    else if (window.self !== window.top) return;
    else if (document.body.clientWidth < 850) return;

    // feature (HTL): home-drive ("H:") button, hardcoded in the template and
    // rendered UNCONDITIONALLY. Upstream-parity lesson (comparing with
    // mickael/kerjean/filestash): withInstantLoad() clones the sidebar DOM
    // on destroy and replays it on every re-render. A button built AFTER an
    // async session fetch misses the FIRST render -> gets baked out of the
    // cache -> "randomly" vanishes (visible only where the fetch won the
    // race). So: static markup from the first paint + the click handler
    // resolves /files/Users/<username> at CLICK time from the session. The
    // label is filled in-place once the session arrives (same node, cache
    // stays consistent). No user? click routes through login like any fetch.
    const homeButton = `
        <ul class="htl-quickshare">
            <li class="no-select">
                <a data-htl-home="link" href="" draggable="false" aria-selected="false" title="My Home Drive (H:)">
                    <img class="component_icon" src="${HTL_HOME_ICON}" alt="directory" draggable="false">
                    <div class="ellipsis">Home</div>
                </a>
            </li>
        </ul>`;

    const $sidebar = render(createElement(`
        <div class="component_sidebar"><div>
            <h3 class="no-select htl-sidebar-brand">${HTL_LOGO_HTML}
            </h3>
            <div data-bind="your-files">
                ${generateSkeleton(2)}
            </div>
            ${homeButton}
            <div data-bind="your-tags">
                ${generateSkeleton(2)}
            </div>
        </div>
    `));
    withInstantLoad($sidebar);
    withResize($sidebar);

    // feature (HTL): resolve the home button's label + target from the
    // session, in place (the node identity is stable: template-rendered).
    // The router's global data-link handler only follows clicks carrying
    // [data-link]; ours sets the href BEFORE navigation, so: intercept the
    // click, resolve, then run navigate() directly (href stays empty until
    // then, so a middle-hover shows nothing bogus).
    const $homeLink = qs($sidebar, `[data-htl-home="link"]`);
    $homeLink.onclick = async(e) => {
        e.preventDefault();
        e.stopPropagation();
        try {
            const session = await getSession().pipe(rxjs.first()).toPromise();
            const home = (session || {}).home || "";
            const u = htlHomeUser(home) || (home.split("/").filter((c) => c !== "")[0] || "");
            if (u) {
                qs($homeLink, ".ellipsis").textContent = u;
                $homeLink.setAttribute("title", "/Users/" + u + "/");
                return navigate(toHref("/files/Users/" + encodeURIComponent(u).replaceAll("%2F", "/")));
            }
        } catch (err) {}
        window.location.href = toHref("/login?next=" + location.pathname); // no session: login first
    };

    const path = getCurrentPath("(/view/|/files/)");

    // fature: file navigation pane
    const $files = qs($sidebar, `[data-bind="your-files"]`);
    ctrlNavigationPane(createRender($files), { $sidebar, path });

    // feature: tag viewer
    const $tags = qs($sidebar, `[data-bind="your-tags"]`);
    effect(rxjs.merge(
        rxjs.of(null),
        rxjs.fromEvent(window, "filestash::tag"),
    ).pipe(
        rxjs.tap(() => ctrlTagPane(createRender($tags), {
            tags: [...$tags.querySelectorAll("a")].map(($tag) => $tag.innerText.trim()),
            path,
        })),
    ));

    // feature: visibility of the sidebar
    const isVisible = () => settingsGet({ visible: true }, "sidebar").visible;
    const forceRefresh = () => window.dispatchEvent(new Event("resize"));
    effect(rxjs.merge(rxjs.fromEvent(window, "keydown")).pipe(
        rxjs.filter((e) => e.key === "b" && e.ctrlKey === true),
        rxjs.tap(() => {
            settingsSave({ visible: $sidebar.classList.contains("hidden") }, "sidebar");
            isVisible() ? $sidebar.classList.remove("hidden") : $sidebar.classList.add("hidden");
            forceRefresh();
        }),
    ));
    effect(rxjs.merge(
        rxjs.fromEvent(window, "resize"),
        rxjs.of(null),
    ).pipe(
        rxjs.tap(() => {
            const $breadcrumbButton = qs(document.body, "[alt=\"sidebar-open\"]");
            if (document.body.clientWidth < 1100) $sidebar.classList.add("hidden");
            else if (isVisible()) {
                $sidebar.classList.remove("hidden");
                $breadcrumbButton.classList.add("hidden");
            } else {
                $sidebar.classList.add("hidden");
                $breadcrumbButton.classList.remove("hidden");
            }
        }),
        rxjs.catchError((err) => {
            if (err instanceof DOMException) return rxjs.EMPTY;
            throw err;
        }),
    ));
    effect(onClick(qs($sidebar, `img[alt="close"]`)).pipe(
        rxjs.tap(() => {
            settingsSave({ visible: false }, "sidebar");
            $sidebar.classList.add("hidden");
            forceRefresh();
        }),
    ));
}

const withResize = (function() {
    let memory = null;
    return ($sidebar) => {
        const $resize = createElement(`<div class="resizer"></div>`);
        effect(rxjs.fromEvent($resize, "mousedown").pipe(
            preventDefault(),
            rxjs.mergeMap((e0) => rxjs.fromEvent(document, "mousemove").pipe(
                rxjs.takeUntil(rxjs.fromEvent(document, "mouseup")),
                rxjs.startWith(e0),
                rxjs.pairwise(),
                rxjs.map(([prevX, currX]) => currX.clientX - prevX.clientX),
                rxjs.scan((width, delta) => width + delta, $sidebar.offsetWidth),
            )),
            rxjs.startWith(memory),
            rxjs.filter((w) => !!w),
            rxjs.map((w) => Math.min(Math.max(w, 250), 400)),
            rxjs.tap((w) => {
                $sidebar.style.width = `${w}px`;
                memory = w;
            }),
        ));
        $sidebar.appendChild($resize);
    };
}());

const withInstantLoad = (function() {
    const state = { scrollTop: 0, $cache: null };
    return ($sidebar) => {
        if (state.$cache) {
            $sidebar.replaceChildren(state.$cache);
            $sidebar.firstElementChild.scrollTop = state.scrollTop;
        }
        onDestroy(() => {
            state.$cache = $sidebar.firstElementChild?.cloneNode(true);
            state.scrollTop = $sidebar.firstElementChild.scrollTop;
        });
    };
}());

export function init() {
    return loadCSS(import.meta.url, "./sidebar.css");
}
