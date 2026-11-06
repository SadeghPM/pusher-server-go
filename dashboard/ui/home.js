(() => {
    'use strict';

    const prefersReducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

    /* ============================================================
       Header elevation
       ============================================================ */
    const header = document.getElementById('site-header');
    const syncHeader = () => {
        if (header) header.classList.toggle('is-scrolled', window.scrollY > 8);
    };
    syncHeader();
    window.addEventListener('scroll', syncHeader, { passive: true });

    /* ============================================================
       Mobile menu
       ============================================================ */
    const navToggle = document.getElementById('nav-toggle');
    const mobileMenu = document.getElementById('mobile-menu');

    function setMenu(open) {
        if (!navToggle || !mobileMenu) return;
        mobileMenu.hidden = !open;
        navToggle.setAttribute('aria-expanded', String(open));
        navToggle.setAttribute('aria-label', open ? 'Close menu' : 'Open menu');
    }

    if (navToggle && mobileMenu) {
        navToggle.addEventListener('click', () => setMenu(mobileMenu.hidden));
        mobileMenu.addEventListener('click', (event) => {
            if (event.target.closest('a')) setMenu(false);
        });
        document.addEventListener('keydown', (event) => {
            if (event.key === 'Escape' && !mobileMenu.hidden) {
                setMenu(false);
                navToggle.focus();
            }
        });
        document.addEventListener('click', (event) => {
            if (!mobileMenu.hidden && !event.target.closest('.site-header')) setMenu(false);
        });
        window.addEventListener('resize', () => {
            if (window.innerWidth > 960) setMenu(false);
        });
    }

    /* ============================================================
       Quick start tabs
       ============================================================ */
    const tabs = Array.from(document.querySelectorAll('[role="tab"]'));

    function selectTab(tab) {
        tabs.forEach((candidate) => {
            const selected = candidate === tab;
            candidate.setAttribute('aria-selected', String(selected));
            candidate.tabIndex = selected ? 0 : -1;

            const panel = document.getElementById(candidate.getAttribute('aria-controls'));
            if (panel) panel.hidden = !selected;
        });
    }

    tabs.forEach((tab, index) => {
        tab.addEventListener('click', () => selectTab(tab));
        tab.addEventListener('keydown', (event) => {
            let next = null;
            if (event.key === 'ArrowRight') next = tabs[(index + 1) % tabs.length];
            if (event.key === 'ArrowLeft') next = tabs[(index - 1 + tabs.length) % tabs.length];
            if (event.key === 'Home') next = tabs[0];
            if (event.key === 'End') next = tabs[tabs.length - 1];
            if (!next) return;
            event.preventDefault();
            next.focus();
            selectTab(next);
        });
    });

    /* ============================================================
       Copy buttons
       ============================================================ */
    async function copyText(text) {
        if (navigator.clipboard && window.isSecureContext) {
            await navigator.clipboard.writeText(text);
            return;
        }

        const area = document.createElement('textarea');
        area.value = text;
        area.setAttribute('readonly', '');
        area.style.position = 'fixed';
        area.style.top = '-1000px';
        area.style.opacity = '0';
        document.body.appendChild(area);
        area.select();
        try {
            document.execCommand('copy');
        } finally {
            area.remove();
        }
    }

    document.querySelectorAll('[data-copy-target]').forEach((button) => {
        button.addEventListener('click', async () => {
            const target = document.getElementById(button.dataset.copyTarget);
            if (!target) return;

            const original = button.textContent;
            try {
                await copyText(target.textContent.replace(/\u00a0/g, ' '));
                button.textContent = 'Copied';
                button.classList.add('is-copied');
            } catch (error) {
                button.textContent = 'Copy failed';
            }
            window.setTimeout(() => {
                button.textContent = original;
                button.classList.remove('is-copied');
            }, 1600);
        });
    });

    /* ============================================================
       Decorative live event loop
       ============================================================ */
    const heroEvents = document.getElementById('hero-events');

    if (heroEvents && !prefersReducedMotion) {
        const samples = [
            { badge: 'badge-success', type: 'connection', name: 'client connected', meta: 'socket 842.120' },
            { badge: 'badge-info', type: 'subscription', name: 'presence-chat.7', meta: 'socket 842.120' },
            { badge: 'badge-accent', type: 'api_message', name: 'OrderShipped', meta: 'channel private-orders.42' },
            { badge: 'badge-warning', type: 'client_event', name: 'typing', meta: 'channel presence-chat.7' },
            { badge: 'badge-violet', type: 'webhook_success', name: 'order.shipped', meta: '200 · 24ms' },
            { badge: 'badge-danger', type: 'disconnection', name: 'client disconnected', meta: 'socket 842.113' }
        ];

        let sampleIndex = 0;
        let tick = 14 * 3600 + 32 * 60 + 14;

        const clock = () => {
            tick += 1 + Math.floor(Math.random() * 3);
            const hours = String(Math.floor(tick / 3600) % 24).padStart(2, '0');
            const minutes = String(Math.floor((tick % 3600) / 60)).padStart(2, '0');
            const seconds = String(tick % 60).padStart(2, '0');
            return `${hours}:${minutes}:${seconds}`;
        };

        const appendEvent = () => {
            if (document.hidden) return;

            const sample = samples[sampleIndex % samples.length];
            sampleIndex += 1;

            const row = document.createElement('div');
            row.className = 'hero-event is-new';
            row.innerHTML = `
                <span class="time">${clock()}</span>
                <div>
                    <div class="line"><span class="badge ${sample.badge}">${sample.type}</span><span class="name">${sample.name}</span></div>
                    <div class="meta">${sample.meta}</div>
                </div>
            `;

            heroEvents.appendChild(row);
            while (heroEvents.children.length > 4) {
                heroEvents.removeChild(heroEvents.firstElementChild);
            }
        };

        window.setInterval(appendEvent, 3200);
    }

    /* ============================================================
       Footer year
       ============================================================ */
    const year = document.getElementById('year');
    if (year) year.textContent = String(new Date().getFullYear());
})();
