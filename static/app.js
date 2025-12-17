// This should be done ASAP and can be done before DOMContentLoaded to prevent FOUC
(function(){
    const jsRequredStyle = document.createElement('style');
    jsRequredStyle.textContent = '.js-required {display: initial}';
    document.head.appendChild(jsRequredStyle);
})();

(function () {
    // Let the server know that we are using JavaScript so we can dynamically load content
    // The server will clear the cookie upon sending a page response,
    // and then when the client loads the page, it sets this cookie for the next request.
    document.cookie = "js=1; Path=/; SameSite=Strict";

    // Hotkeys
    document.addEventListener('keydown', function (event) {
        // Ignore modifier keys and IME
        if (event.metaKey || event.altKey || event.isComposing) {
            return;
        }

        // Check if the key is being pressed in an input or textarea
        const target = event.target.tagName.toLowerCase();
        if (['input', 'textarea'].includes(target) && event.target.type !== 'checkbox') {
            return;
        }

        if (event.shiftKey) {
            if (event.key === 'J') {
                let autojumpEl = document.querySelector('input#auto-jump-checkbox');
                if (autojumpEl) {
                    autojumpEl.click();
                    return;
                }
            }
            return;
        }

        if (event.ctrlKey) {
            if (event.key === '/') {
                let searchEl = document.querySelector('input#search');
                if (searchEl) {
                    searchEl.focus();
                    return;
                }
            }
            return;
        }

        switch (event.key) {
            case 'f':
                document.location = '/random/file';
                break;

            case 'g':
                document.location = '/random/gallery';
                break;

            case 'i':
                let expandEl = document.querySelector('input#fb-expand');
                if (expandEl) {
                    expandEl.checked = !expandEl.checked;
                }
                break;

            case 'j':
                let jumpEl = document.querySelector('a#jump-to-content-link');
                let mainContentEl = document.querySelector('a#main-content');
                if (jumpEl && mainContentEl) {
                    jumpEl.click();
                }
                break;

            case 'k':
                let topEl = document.querySelector('a#top');
                if (topEl) {
                    topEl.click();
                }
                break;

            case 'ArrowLeft':
                // Do not handle hotkeys that the browser uses for video
                if (target === 'video') {
                    return;
                }
            // fallthrough
            case 'h':
                let prevFileEl = document.querySelector('.pv-rail .pv-prev a:last-child');
                let prevPageEl = document.querySelector('a.pager-prev');
                if (prevFileEl) {
                    prevFileEl.click();
                } else if (prevPageEl) {
                    prevPageEl.click();
                }
                break;

            case 'ArrowRight':
                // Do not handle hotkeys that the browser uses for video
                if (target === 'video') {
                    return;
                }
            // fallthrough
            case 'l':
                let nextFileEl = document.querySelector('.pv-rail .pv-next a:first-child');
                let nextPageEl = document.querySelector('a.pager-next');
                if (nextFileEl) {
                    nextFileEl.click();
                } else if (nextPageEl) {
                    nextPageEl.click();
                }
                break;
        }
    });

    function scrollIntoView(el, instant) {
        // smooth scrollIntoView is bugged on Chrome; it sometimes stops scrolling too soon
        // smooth only works if enabled in css; currently disabled
        const behavior = !instant ? 'smooth' : 'instant';
        el.scrollIntoView({behavior: behavior, block: 'start', inline: 'start'});
    }

    function focusVidEl() {
        let vidEl;
        vidEl = document.querySelector('video.main-content-resource');
        if (vidEl) {
            setTimeout(function () {
                vidEl.focus();
            }, 1);
        }
    }

    function goJump() {
        // Adds to history
        window.location.hash = '#main-content';

        // Doesn't update :target
        history.replaceState(null, null, '#main-content');
        const mainContentEl = document.querySelector('#main-content');
        if (mainContentEl) {
            scrollIntoView(mainContentEl);
        }
    }

    function handleJump(event) {
        // It works without JS, but if JS is enabled, we can keep a clean history and update focus
        goJump();
        focusVidEl();
    }

    function goTop() {
        // Adds to history
        // window.location.hash = '#top';

        // Doesn't update :target
        history.replaceState(null, null, '#top');
        const topEl = document.querySelector('a#top');
        if (topEl) {
            scrollIntoView(topEl);
        }
    }

    function handleTop(event) {
        goTop();
    }

    function autoJump() {
        // Jump to content on page load
        const checkbox = document.getElementById('auto-jump-checkbox');
        const savedState = localStorage.getItem('autoJumpChecked');
        if (savedState) {
            checkbox.checked = JSON.parse(savedState);

            const mainContentEl = document.querySelector('#main-content');
            const imgEl = document.querySelector('img.main-content-resource');
            const vidEl = document.querySelector('video.main-content-resource');

            // If auto-jump is enabled, jump to content immediately
            if (checkbox.checked && /^(|#|#main-content)$/.test(window.location.hash) && mainContentEl && (imgEl || vidEl)) {
                function jump() {
                    const htmlEl = document.querySelector('html');
                    let htmlWasScrollSmooth = htmlEl.classList.contains('scroll-smooth');
                    if (htmlWasScrollSmooth) {
                        htmlEl.classList.remove('scroll-smooth');
                    }
                    // Adds to history:
                    // window.location.hash = '';
                    window.location.hash = '#main-content'; // replaceState doesn't set :target on the initial page load on Firefox or  Chrome
                    history.replaceState(null, null, '#main-content');
                    scrollIntoView(mainContentEl, true); // instant scroll on initial page load
                    if (htmlWasScrollSmooth) {
                        htmlEl.classList.add('scroll-smooth');
                    }
                }

                if (imgEl) {
                    if (imgEl.complete) {
                        jump();
                    } else {
                        imgEl.addEventListener('load', event => jump());
                    }
                }
                if (vidEl) {
                    if (vidEl.readyState === HTMLMediaElement.HAVE_METADATA) {
                        jump();
                        // Enable space bar to play after jumping to video
                        focusVidEl();
                    } else {
                        vidEl.addEventListener('loadedmetadata', event => {
                            jump();
                            focusVidEl();
                        });
                    }
                }
            }
        }
    }

    function setupAutoJumpChangeListener() {
        const checkbox = document.getElementById('auto-jump-checkbox');
        // Save state when checkbox changes
        checkbox.addEventListener('change', function () {
            localStorage.setItem('autoJumpChecked', JSON.stringify(this.checked));
            const mainContentEl = document.querySelector('#main-content');
            if (this.checked && mainContentEl && /^(#|#main-content|)$/.test(window.location.hash)) {
                // Jump to content if checked and hash doesn't exist in URL
                // Adds to history:
                // window.location.hash = '';
                // window.location.hash = '#main-content';
                history.replaceState(null, null, '#main-content');
                scrollIntoView(mainContentEl);
                focusVidEl();
            }
        });
    }

    function setupCellNavBtnTrackPointer() {
        const cellNavEls = document.querySelectorAll('.cell-nav');
        cellNavEls.forEach(cellNavEl => {
            const cellNavBtnEl = cellNavEl.querySelector('.cell-nav-btn');
            if (cellNavBtnEl) {
                let overrideStylesSet = false;

                cellNavEl.addEventListener('pointermove', event => {
                    // works with display: sticky, but translate is supposed to be better for performance / battery
                    // cellNavBtnEl.style.top = Math.max(0, event.clientY - cellNavBtnEl.clientHeight / 2) + 'px';

                    if (!overrideStylesSet) {
                        // needed for the translate approach
                        cellNavBtnEl.style.position = 'relative';
                        cellNavBtnEl.style.top = '0';
                        overrideStylesSet = true;
                    }
                    let layerY = event.clientY - cellNavEl.getBoundingClientRect().top;
                    let y = Math.max(0,
                        Math.min(cellNavEl.clientHeight - cellNavBtnEl.clientHeight,
                            layerY - cellNavBtnEl.clientHeight / 2));
                    cellNavBtnEl.style.transform = `translateY(${y}px)`;
                });
            }
        });
    }

    document.addEventListener('DOMContentLoaded', function () {
        const jumpEl = document.querySelector('a#jump-to-content-link');
        jumpEl.addEventListener('click', handleJump);
        const topEl = document.querySelector('a#top');
        topEl.addEventListener('click', handleTop);
        const backEls = document.querySelectorAll('a.onclick-back');
        backEls.forEach(el => {
            el.addEventListener('click', () => history.back());
        });

        setupCellNavBtnTrackPointer();

        autoJump();
        setupAutoJumpChangeListener();
    });
})();
