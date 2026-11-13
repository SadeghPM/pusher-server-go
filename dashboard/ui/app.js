(() => {
    'use strict';

    /* ============================================================
       Constants & storage helpers
       ============================================================ */

    const TOKEN_KEY = 'pusher_admin_token';
    const THEME_KEY = 'pusher_dashboard_theme';
    const ACTIVE_APP_KEY = 'pusher_active_app';
    const MAX_EVENTS = 200;
    const MAX_RECONNECT_DELAY = 15000;

    const $ = (id) => document.getElementById(id);

    function storageGet(key) {
        try {
            return localStorage.getItem(key);
        } catch (error) {
            return null;
        }
    }

    function storageSet(key, value) {
        try {
            localStorage.setItem(key, value);
        } catch (error) {
            /* storage can be unavailable in private contexts */
        }
    }

    function storageRemove(key) {
        try {
            localStorage.removeItem(key);
        } catch (error) {
            /* ignore */
        }
    }

    /* ============================================================
       DOM references
       ============================================================ */

    const loginView = $('login-view');
    const appShell = $('app');
    const loginForm = $('login-form');
    const adminTokenInput = $('admin-token');
    const toggleTokenButton = $('toggle-token');
    const loginError = $('login-error');
    const loginErrorText = $('login-error-text');
    const saveTokenButton = $('save-token');

    const sidebar = $('sidebar');
    const sidebarToggle = $('sidebar-toggle');
    const sidebarClose = $('sidebar-close');
    const sidebarBackdrop = $('sidebar-backdrop');

    const appSelector = $('app-selector');
    const tenantCount = $('tenant-count');
    const btnAddTenant = $('btn-add-tenant');
    const btnAddTenantEmpty = $('btn-add-tenant-empty');
    const noTenants = $('no-tenants');
    const workspaceContent = $('workspace-content');

    const navDebug = $('nav-debug');
    const navOverview = $('nav-overview');
    const viewDebug = $('view-debug');
    const viewOverview = $('view-overview');

    const topbarApp = $('topbar-app');
    const wsStatus = $('ws-status');
    const wsStatusText = $('ws-status-text');
    const themeToggle = $('theme-toggle');
    const logoutButton = $('logout');

    const debugAppLabel = $('debug-app-label');
    const consoleApp = $('console-app');
    const btnOpenCreator = $('btn-open-creator');
    const btnPause = $('btn-pause');
    const pauseIcon = $('pause-icon');
    const pauseText = $('pause-text');
    const pauseCount = $('pause-count');
    const btnClear = $('btn-clear');
    const searchInput = $('search-events');
    const clearSearchButton = $('clear-search');
    const eventCount = $('event-count');
    const eventLog = $('event-log');

    const settingsAppLabel = $('settings-app-label');
    const overviewAppId = $('overview-app-id');
    const overviewKey = $('overview-key');
    const overviewSecret = $('overview-secret');
    const overviewHost = $('overview-host');
    const toggleSecretButton = $('toggle-secret');
    const overviewOrigins = $('overview-origins');
    const overviewWebhooks = $('overview-webhooks');
    const configForm = $('config-form');
    const configError = $('config-save-error');
    const configErrorText = $('config-save-error-text');
    const saveConfigButton = $('btn-save-config');
    const dangerAppName = $('danger-app-name');
    const btnDeleteTenant = $('btn-delete-tenant');

    const serverStatus = $('server-status');
    const serverStatusText = $('server-status-text');
    const serverStatusSub = $('server-status-sub');

    const tenantModal = $('tenant-modal');
    const tenantForm = $('tenant-form');
    const tenantAppIdInput = $('tenant-app-id');
    const tenantError = $('tenant-error');
    const tenantErrorText = $('tenant-error-text');
    const slugError = $('slug-error');
    const createTenantButton = $('btn-create-tenant');

    const creatorModal = $('creator-modal');
    const creatorForm = $('creator-form');
    const creatorChannel = $('creator-channel');
    const creatorEvent = $('creator-event');
    const creatorData = $('creator-data');
    const creatorError = $('creator-error');
    const creatorErrorText = $('creator-error-text');
    const sendEventButton = $('btn-send-event');

    const deleteModal = $('delete-modal');
    const deleteAppName = $('delete-app-name');
    const deleteError = $('delete-error');
    const deleteErrorText = $('delete-error-text');
    const confirmDeleteButton = $('btn-confirm-delete');

    const toastRoot = $('toasts');

    /* ============================================================
       State
       ============================================================ */

    const state = {
        token: '',
        apps: [],
        activeAppId: null,
        events: [],
        pausedBuffer: [],
        paused: false,
        view: 'debug',
        socket: null,
        socketGeneration: 0,
        reconnectTimer: null,
        reconnectAttempts: 0,
        shouldReconnect: false,
        healthTimer: null,
        lastFocus: null,
        secretVisible: false,
        appToDelete: null,
        renderFrame: null
    };

    /* ============================================================
       Utilities
       ============================================================ */

    function escapeHtml(value) {
        return String(value == null ? '' : value)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    function activeApp() {
        return state.apps.find((app) => app.app_id === state.activeAppId) || null;
    }

    function replaceApp(updated) {
        const index = state.apps.findIndex((app) => app.app_id === updated.app_id);
        if (index !== -1) state.apps[index] = updated;
    }

    function setButtonLoading(button, loading, label) {
        if (!button) return;

        if (loading) {
            if (!button.dataset.originalHtml) button.dataset.originalHtml = button.innerHTML;
            button.disabled = true;
            button.innerHTML = `<span class="spinner" aria-hidden="true"></span>${escapeHtml(label || 'Working…')}`;
            return;
        }

        button.disabled = false;
        if (button.dataset.originalHtml) {
            button.innerHTML = button.dataset.originalHtml;
            delete button.dataset.originalHtml;
        }
    }

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

    function readFieldValue(element) {
        if (!element) return '';
        if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement) {
            return element.value;
        }
        return element.textContent || '';
    }

    /* ============================================================
       Toasts
       ============================================================ */

    const TOAST_ICONS = {
        success: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="m5 12 5 5L20 7"></path></svg>',
        error: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"></circle><path d="M12 8v5"></path><path d="M12 16h.01"></path></svg>',
        info: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"></circle><path d="M12 11v5"></path><path d="M12 8h.01"></path></svg>'
    };

    function toast(message, type = 'success') {
        if (!toastRoot) return;

        while (toastRoot.children.length >= 4) {
            toastRoot.removeChild(toastRoot.firstElementChild);
        }

        const element = document.createElement('div');
        element.className = `toast toast-${type}`;
        element.innerHTML = `
            <div class="toast-icon">${TOAST_ICONS[type] || TOAST_ICONS.info}</div>
            <div class="toast-message"></div>
            <button class="toast-close" type="button" aria-label="Dismiss notification">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6 6 18M6 6l12 12"></path></svg>
            </button>
        `;
        element.querySelector('.toast-message').textContent = message;

        let removed = false;
        const remove = () => {
            if (removed) return;
            removed = true;
            element.classList.add('is-leaving');
            window.setTimeout(() => element.remove(), 180);
        };

        element.querySelector('.toast-close').addEventListener('click', remove);
        toastRoot.appendChild(element);
        window.setTimeout(remove, 4200);
    }

    /* ============================================================
       Theme
       ============================================================ */

    function applyTheme(theme) {
        const next = theme === 'light' ? 'light' : 'dark';
        document.documentElement.setAttribute('data-theme', next);
        storageSet(THEME_KEY, next);

        const meta = $('meta-theme-color');
        if (meta) meta.setAttribute('content', next === 'light' ? '#f7f8f3' : '#08090c');

        if (themeToggle) {
            themeToggle.setAttribute(
                'aria-label',
                next === 'light' ? 'Switch to dark theme' : 'Switch to light theme'
            );
        }
    }

    function initTheme() {
        const saved = storageGet(THEME_KEY);
        applyTheme(saved === 'light' ? 'light' : 'dark');

        if (themeToggle) {
            themeToggle.addEventListener('click', () => {
                const current = document.documentElement.getAttribute('data-theme');
                applyTheme(current === 'light' ? 'dark' : 'light');
            });
        }
    }

    /* ============================================================
       Login / session
       ============================================================ */

    function showLoginError(message) {
        if (!loginError || !loginErrorText) return;
        loginErrorText.textContent = message;
        loginError.hidden = false;
    }

    function clearLoginError() {
        if (!loginError) return;
        loginError.hidden = true;
        if (loginErrorText) loginErrorText.textContent = '';
    }

    function showLogin(message) {
        closeSidebar();
        stopHealthPolling();
        disconnectSocket();
        setWsStatus('idle');

        loginView.hidden = false;
        appShell.hidden = true;
        document.title = 'Admin · Pusher Server Go';

        if (adminTokenInput) adminTokenInput.value = '';
        if (message) {
            showLoginError(message);
        } else {
            clearLoginError();
        }

        window.setTimeout(() => {
            if (!loginView.hidden && adminTokenInput) adminTokenInput.focus();
        }, 40);
    }

    function clearStoredSession() {
        state.token = '';
        storageRemove(TOKEN_KEY);
        storageRemove(ACTIVE_APP_KEY);
    }

    async function authenticate(candidate, { silent = false } = {}) {
        if (!silent) setButtonLoading(saveTokenButton, true, 'Signing in…');
        clearLoginError();

        try {
            const response = await fetch('/api/apps', {
                headers: { Authorization: `Bearer ${candidate}` },
                cache: 'no-store'
            });

            if (response.status === 401) {
                clearStoredSession();
                showLogin('That token is not valid. Check admin_token in config.yaml.');
                return;
            }

            if (!response.ok) {
                throw new Error(`The dashboard returned ${response.status}. Try again.`);
            }

            const apps = await response.json();
            state.token = candidate;
            state.apps = Array.isArray(apps) ? apps : [];
            storageSet(TOKEN_KEY, candidate);

            clearLoginError();
            loginView.hidden = true;
            appShell.hidden = false;

            initWorkspace();

            if (!silent) toast('Signed in', 'success');
        } catch (error) {
            const message = error && error.message
                ? error.message
                : 'Could not reach the dashboard API. Check that the server is running.';
            showLogin(message);
        } finally {
            if (!silent) setButtonLoading(saveTokenButton, false);
        }
    }

    function handleSessionExpired() {
        clearStoredSession();
        state.apps = [];
        state.activeAppId = null;
        showLogin('Your session expired. Sign in again.');
    }

    async function apiFetch(url, options = {}) {
        const headers = new Headers(options.headers || {});
        headers.set('Authorization', `Bearer ${state.token}`);

        const response = await fetch(url, {
            ...options,
            headers,
            cache: 'no-store'
        });

        if (response.status === 401) {
            handleSessionExpired();
            throw new Error('Your session expired. Sign in again.');
        }

        return response;
    }

    async function readError(response, fallback) {
        try {
            const text = (await response.text()).trim();
            return text || fallback;
        } catch (error) {
            return fallback;
        }
    }

    /* ============================================================
       Server health
       ============================================================ */

    function resetHealthStatus() {
        if (!serverStatus) return;
        serverStatus.classList.remove('is-online', 'is-offline');
        if (serverStatusText) serverStatusText.textContent = 'Checking server…';
        if (serverStatusSub) serverStatusSub.textContent = 'Pusher protocol v7';
    }

    async function checkHealth() {
        try {
            const response = await fetch('/api/health', { cache: 'no-store' });
            if (!response.ok) throw new Error('Health check failed');
            const data = await response.json();

            serverStatus.classList.remove('is-offline');
            serverStatus.classList.add('is-online');
            serverStatusText.textContent = 'Server online';
            serverStatusSub.textContent = data.port ? `Realtime :${data.port}` : 'Realtime server';
        } catch (error) {
            serverStatus.classList.remove('is-online');
            serverStatus.classList.add('is-offline');
            serverStatusText.textContent = 'Server unreachable';
            serverStatusSub.textContent = 'Check the process logs';
        }
    }

    function startHealthPolling() {
        stopHealthPolling();
        resetHealthStatus();
        checkHealth();
        state.healthTimer = window.setInterval(checkHealth, 30000);
    }

    function stopHealthPolling() {
        if (state.healthTimer) {
            window.clearInterval(state.healthTimer);
            state.healthTimer = null;
        }
    }

    /* ============================================================
       Sidebar drawer
       ============================================================ */

    function openSidebar() {
        document.body.classList.add('sidebar-open');
        if (sidebarToggle) sidebarToggle.setAttribute('aria-expanded', 'true');
        window.setTimeout(() => {
            if (sidebarClose && window.innerWidth <= 1024) sidebarClose.focus();
        }, 40);
    }

    function closeSidebar({ restoreFocus = false } = {}) {
        const wasOpen = document.body.classList.contains('sidebar-open');
        document.body.classList.remove('sidebar-open');
        if (sidebarToggle) sidebarToggle.setAttribute('aria-expanded', 'false');

        if (wasOpen && restoreFocus && sidebarToggle && window.innerWidth <= 1024) {
            sidebarToggle.focus();
        }
    }

    if (sidebarToggle) {
        sidebarToggle.addEventListener('click', () => {
            if (document.body.classList.contains('sidebar-open')) {
                closeSidebar({ restoreFocus: true });
            } else {
                openSidebar();
            }
        });
    }
    if (sidebarClose) sidebarClose.addEventListener('click', () => closeSidebar({ restoreFocus: true }));
    if (sidebarBackdrop) sidebarBackdrop.addEventListener('click', () => closeSidebar({ restoreFocus: true }));

    const desktopQuery = window.matchMedia('(min-width: 1025px)');
    if (desktopQuery.addEventListener) {
        desktopQuery.addEventListener('change', (event) => {
            if (event.matches) closeSidebar();
        });
    }

    /* ============================================================
       Modals
       ============================================================ */

    function openModal(modal, focusTarget) {
        if (!modal) return;

        state.lastFocus = document.activeElement;
        modal.hidden = false;
        document.body.classList.add('modal-open');

        const target = focusTarget || modal.querySelector('input, textarea, button');
        if (target) {
            window.setTimeout(() => target.focus(), 40);
        }
    }

    function closeModal(modal) {
        if (!modal) return;

        modal.hidden = true;
        if (!document.querySelector('.modal:not([hidden])')) {
            document.body.classList.remove('modal-open');
        }

        if (state.lastFocus && document.contains(state.lastFocus)) {
            state.lastFocus.focus();
        }
        state.lastFocus = null;
    }

    document.querySelectorAll('.modal').forEach((modal) => {
        modal.addEventListener('click', (event) => {
            if (event.target === modal) closeModal(modal);
        });
    });

    document.querySelectorAll('[data-close-modal]').forEach((button) => {
        button.addEventListener('click', () => closeModal(button.closest('.modal')));
    });

    document.addEventListener('keydown', (event) => {
        if (event.key === 'Escape') {
            const openModalElement = document.querySelector('.modal:not([hidden])');
            if (openModalElement) {
                event.preventDefault();
                closeModal(openModalElement);
                return;
            }
            if (document.body.classList.contains('sidebar-open')) closeSidebar({ restoreFocus: true });
            return;
        }

        if (event.key !== 'Tab') return;

        const openModalElement = document.querySelector('.modal:not([hidden])');
        if (!openModalElement) return;

        const focusables = Array.from(openModalElement.querySelectorAll(
            'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'
        )).filter((element) => element.offsetParent !== null);

        if (focusables.length === 0) return;

        const first = focusables[0];
        const last = focusables[focusables.length - 1];

        if (event.shiftKey && document.activeElement === first) {
            event.preventDefault();
            last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault();
            first.focus();
        }
    });

    /* ============================================================
       Navigation
       ============================================================ */

    function viewFromHash() {
        return window.location.hash === '#/overview' ? 'overview' : 'debug';
    }

    function updateDocumentTitle() {
        const viewLabel = state.view === 'overview' ? 'Overview' : 'Live events';
        document.title = state.activeAppId
            ? `${viewLabel} · ${state.activeAppId} · Pusher Server Go`
            : `${viewLabel} · Pusher Server Go`;
    }

    function navigate(view, { updateHash = true } = {}) {
        state.view = view === 'overview' ? 'overview' : 'debug';
        const isDebug = state.view === 'debug';

        navDebug.classList.toggle('is-active', isDebug);
        navOverview.classList.toggle('is-active', !isDebug);

        if (isDebug) {
            navDebug.setAttribute('aria-current', 'page');
            navOverview.removeAttribute('aria-current');
        } else {
            navOverview.setAttribute('aria-current', 'page');
            navDebug.removeAttribute('aria-current');
        }

        viewDebug.hidden = !isDebug;
        viewOverview.hidden = isDebug;

        if (state.view === 'overview') renderOverview();

        if (updateHash && window.location.hash !== `#/${state.view}`) {
            window.history.pushState(null, '', `#/${state.view}`);
        }

        updateDocumentTitle();
        closeSidebar();
    }

    function goToView(view) {
        const target = view === 'overview' ? 'overview' : 'debug';
        if (window.location.hash === `#/${target}`) {
            navigate(target);
        } else {
            window.location.hash = `#/${target}`;
        }
    }

    navDebug.addEventListener('click', (event) => {
        event.preventDefault();
        goToView('debug');
    });

    navOverview.addEventListener('click', (event) => {
        event.preventDefault();
        goToView('overview');
    });

    window.addEventListener('hashchange', () => navigate(viewFromHash(), { updateHash: false }));

    /* ============================================================
       Tenants / app selector
       ============================================================ */

    function populateAppSelector() {
        appSelector.innerHTML = '';

        if (state.apps.length === 0) {
            const option = document.createElement('option');
            option.textContent = 'No tenants';
            option.value = '';
            appSelector.appendChild(option);
            appSelector.disabled = true;
        } else {
            state.apps.forEach((app) => {
                const option = document.createElement('option');
                option.value = app.app_id;
                option.textContent = app.name || app.app_id;
                appSelector.appendChild(option);
            });
            appSelector.disabled = false;
        }

        if (state.activeAppId) appSelector.value = state.activeAppId;
        if (tenantCount) tenantCount.textContent = String(state.apps.length);
    }

    function syncActiveAppUI() {
        const app = activeApp();
        const label = app ? app.app_id : '—';

        if (appSelector && app) appSelector.value = app.app_id;
        if (topbarApp) topbarApp.textContent = label;
        if (debugAppLabel) debugAppLabel.textContent = label;
        if (consoleApp) consoleApp.textContent = label;
        if (settingsAppLabel) settingsAppLabel.textContent = label;
        if (dangerAppName) dangerAppName.textContent = app ? app.app_id : 'this tenant';

        updateDocumentTitle();
    }

    function showNoTenants(isEmpty) {
        noTenants.hidden = !isEmpty;
        workspaceContent.hidden = isEmpty;
    }

    function selectApp(appId, { connect = true, resetStream = true } = {}) {
        const app = state.apps.find((candidate) => candidate.app_id === appId);
        if (!app) return;

        state.activeAppId = app.app_id;
        storageSet(ACTIVE_APP_KEY, app.app_id);
        state.secretVisible = false;

        populateAppSelector();
        syncActiveAppUI();
        showNoTenants(false);

        if (resetStream) resetEventStream();
        if (connect) connectSocket(app.app_id);
        if (state.view === 'overview') renderOverview();
    }

    appSelector.addEventListener('change', () => {
        selectApp(appSelector.value);
    });

    function initWorkspace() {
        stopHealthPolling();
        disconnectSocket();
        resetEventStream();
        hideConfigError();
        state.secretVisible = false;

        if (state.apps.length > 0) {
            const stored = storageGet(ACTIVE_APP_KEY);
            const initial = state.apps.find((app) => app.app_id === stored) || state.apps[0];

            state.activeAppId = initial.app_id;
            storageSet(ACTIVE_APP_KEY, initial.app_id);
            populateAppSelector();
            syncActiveAppUI();
            showNoTenants(false);

            connectSocket(initial.app_id);
            renderEvents();
            if (viewFromHash() === 'overview') renderOverview();
        } else {
            state.activeAppId = null;
            populateAppSelector();
            syncActiveAppUI();
            showNoTenants(true);
            setWsStatus('idle');
            renderEvents();
        }

        if (!window.location.hash || !/^#\/(debug|overview)$/.test(window.location.hash)) {
            window.history.replaceState(null, '', '#/debug');
        }
        navigate(viewFromHash(), { updateHash: false });
        startHealthPolling();
    }

    /* ============================================================
       Overview
       ============================================================ */

    function showConfigError(message) {
        if (!configError || !configErrorText) return;
        configErrorText.textContent = message;
        configError.hidden = false;
    }

    function hideConfigError() {
        if (!configError) return;
        configError.hidden = true;
        if (configErrorText) configErrorText.textContent = '';
    }

    function renderOverview() {
        const app = activeApp();
        if (!app) return;

        overviewAppId.value = app.app_id || '';
        overviewKey.value = app.app_key || '';
        overviewSecret.value = app.app_secret || '';
        overviewHost.value = app.host || '';
        overviewSecret.type = state.secretVisible ? 'text' : 'password';

        toggleSecretButton.setAttribute('aria-pressed', String(state.secretVisible));
        toggleSecretButton.setAttribute('aria-label', state.secretVisible ? 'Hide secret' : 'Show secret');

        overviewOrigins.value = (app.allowed_origins || []).join('\n');
        overviewWebhooks.value = (app.webhooks || []).join('\n');

        settingsAppLabel.textContent = app.app_id;
        dangerAppName.textContent = app.app_id;

        hideConfigError();
    }

    toggleSecretButton.addEventListener('click', () => {
        state.secretVisible = !state.secretVisible;
        overviewSecret.type = state.secretVisible ? 'text' : 'password';
        toggleSecretButton.setAttribute('aria-pressed', String(state.secretVisible));
        toggleSecretButton.setAttribute('aria-label', state.secretVisible ? 'Hide secret' : 'Show secret');
    });

    document.querySelectorAll('[data-copy-target]').forEach((button) => {
        button.addEventListener('click', async () => {
            const target = $(button.dataset.copyTarget);
            if (!target) return;

            const label = button.querySelector('span');
            const original = label ? label.textContent : button.textContent;

            try {
                await copyText(readFieldValue(target));
                if (label) {
                    label.textContent = 'Copied';
                } else {
                    button.textContent = 'Copied';
                }
                button.classList.add('is-copied');
            } catch (error) {
                toast('Could not copy to clipboard.', 'error');
            }

            window.setTimeout(() => {
                if (label) {
                    label.textContent = original;
                } else {
                    button.textContent = original;
                }
                button.classList.remove('is-copied');
            }, 1600);
        });
    });

    configForm.addEventListener('submit', async (event) => {
        event.preventDefault();

        const app = activeApp();
        if (!app) return;

        hideConfigError();

        const origins = parseLines(overviewOrigins.value);
        const webhooks = parseLines(overviewWebhooks.value);

        const invalidWebhook = webhooks.find((url) => {
            try {
                const parsed = new URL(url);
                return parsed.protocol !== 'http:' && parsed.protocol !== 'https:';
            } catch (error) {
                return true;
            }
        });

        if (invalidWebhook) {
            showConfigError(`Webhook "${invalidWebhook}" is not a valid http(s) URL.`);
            overviewWebhooks.focus();
            return;
        }

        setButtonLoading(saveConfigButton, true, 'Saving…');

        try {
            const response = await apiFetch('/api/apps', {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    app_id: app.app_id,
                    allowed_origins: origins,
                    webhooks
                })
            });

            if (!response.ok) {
                throw new Error(await readError(response, 'Could not save the configuration.'));
            }

            const updated = await response.json();
            replaceApp(updated);
            renderOverview();
            toast('Configuration saved', 'success');
        } catch (error) {
            if (error && error.message) {
                showConfigError(error.message);
                toast(error.message, 'error');
            }
        } finally {
            setButtonLoading(saveConfigButton, false);
        }
    });

    function parseLines(value) {
        return value
            .split(/\r?\n/)
            .map((line) => line.trim())
            .filter(Boolean);
    }

    /* ============================================================
       New tenant
       ============================================================ */

    const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$/;

    function validateSlug(value) {
        if (value.length < 2 || value.length > 64) {
            return 'App ID must be between 2 and 64 characters.';
        }
        if (!SLUG_PATTERN.test(value)) {
            return 'Use lowercase letters, digits and hyphens only, starting and ending with a letter or digit.';
        }
        if (state.apps.some((app) => app.app_id.toLowerCase() === value.toLowerCase())) {
            return 'App ID already exists. Must be unique.';
        }
        return '';
    }

    function openTenantModal() {
        tenantForm.reset();
        hideTenantError();
        slugError.hidden = true;
        slugError.textContent = '';
        openModal(tenantModal, tenantAppIdInput);
    }

    function closeTenantModal() {
        closeModal(tenantModal);
    }

    function showTenantError(message) {
        tenantErrorText.textContent = message;
        tenantError.hidden = false;
    }

    function hideTenantError() {
        tenantError.hidden = true;
        tenantErrorText.textContent = '';
    }

    btnAddTenant.addEventListener('click', openTenantModal);
    btnAddTenantEmpty.addEventListener('click', openTenantModal);

    tenantAppIdInput.addEventListener('input', () => {
        hideTenantError();
        const value = tenantAppIdInput.value.trim();

        if (!value) {
            slugError.hidden = true;
            slugError.textContent = '';
            return;
        }

        const error = validateSlug(value);
        if (error) {
            slugError.textContent = error;
            slugError.hidden = false;
        } else {
            slugError.hidden = true;
            slugError.textContent = '';
        }
    });

    tenantForm.addEventListener('submit', async (event) => {
        event.preventDefault();
        hideTenantError();
        slugError.hidden = true;
        slugError.textContent = '';

        const appId = tenantAppIdInput.value.trim();
        const validationError = validateSlug(appId);

        if (validationError) {
            slugError.textContent = validationError;
            slugError.hidden = false;
            tenantAppIdInput.focus();
            return;
        }

        setButtonLoading(createTenantButton, true, 'Creating…');

        try {
            const response = await apiFetch('/api/apps', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ app_id: appId })
            });

            if (!response.ok) {
                throw new Error(await readError(response, 'Could not create the tenant.'));
            }

            const created = await response.json();
            state.apps.push(created);

            closeTenantModal();
            showNoTenants(false);
            selectApp(created.app_id, { connect: true, resetStream: true });
            navigate('overview');

            toast(`Tenant "${created.app_id}" created`, 'success');
        } catch (error) {
            if (error && error.message) {
                showTenantError(error.message);
            }
        } finally {
            setButtonLoading(createTenantButton, false);
        }
    });

    /* ============================================================
       Send test event
       ============================================================ */

    function openCreatorModal() {
        creatorError.hidden = true;
        creatorErrorText.textContent = '';
        openModal(creatorModal, creatorChannel);
    }

    function closeCreatorModal() {
        closeModal(creatorModal);
    }

    function showCreatorError(message) {
        creatorErrorText.textContent = message;
        creatorError.hidden = false;
    }

    btnOpenCreator.addEventListener('click', openCreatorModal);

    creatorForm.addEventListener('submit', async (event) => {
        event.preventDefault();
        creatorError.hidden = true;

        const app = activeApp();
        if (!app) {
            showCreatorError('Select a tenant before sending an event.');
            return;
        }

        const channel = creatorChannel.value.trim();
        const eventName = creatorEvent.value.trim();

        if (!channel || !eventName) {
            showCreatorError('Channel and event are both required.');
            return;
        }

        const rawData = creatorData.value.trim();
        let data = rawData;

        if (rawData) {
            try {
                data = JSON.parse(rawData);
            } catch (error) {
                data = rawData;
            }
        }

        setButtonLoading(sendEventButton, true, 'Sending…');

        try {
            const response = await apiFetch('/api/trigger', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    app_id: app.app_id,
                    channel,
                    event: eventName,
                    data
                })
            });

            if (!response.ok) {
                throw new Error(await readError(response, 'Could not send the event.'));
            }

            closeCreatorModal();
            toast(`Event "${eventName}" sent to #${channel}`, 'success');
        } catch (error) {
            if (error && error.message) {
                showCreatorError(error.message);
            }
        } finally {
            setButtonLoading(sendEventButton, false);
        }
    });

    /* ============================================================
       Delete tenant
       ============================================================ */

    btnDeleteTenant.addEventListener('click', () => {
        const app = activeApp();
        if (!app) return;

        state.appToDelete = app.app_id;
        deleteAppName.textContent = app.app_id;
        deleteError.hidden = true;
        deleteErrorText.textContent = '';
        openModal(deleteModal, deleteModal.querySelector('.btn-ghost'));
    });

    confirmDeleteButton.addEventListener('click', async () => {
        const appId = state.appToDelete;
        if (!appId) return;

        setButtonLoading(confirmDeleteButton, true, 'Deleting…');
        deleteError.hidden = true;

        try {
            const response = await apiFetch('/api/apps', {
                method: 'DELETE',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ app_id: appId })
            });

            if (!response.ok) {
                throw new Error(await readError(response, 'Could not delete the tenant.'));
            }

            state.apps = state.apps.filter((app) => app.app_id !== appId);
            state.appToDelete = null;

            closeModal(deleteModal);

            if (state.apps.length > 0) {
                const nextApp = state.apps[0];
                selectApp(nextApp.app_id, { connect: true, resetStream: true });
            } else {
                state.activeAppId = null;
                storageRemove(ACTIVE_APP_KEY);
                populateAppSelector();
                syncActiveAppUI();
                showNoTenants(true);
                resetEventStream();
                disconnectSocket();
                setWsStatus('idle');
            }

            toast(`Tenant "${appId}" deleted`, 'success');
        } catch (error) {
            if (error && error.message) {
                deleteErrorText.textContent = error.message;
                deleteError.hidden = false;
            }
        } finally {
            setButtonLoading(confirmDeleteButton, false);
        }
    });

    /* ============================================================
       WebSocket
       ============================================================ */

    function setWsStatus(status) {
        if (!wsStatus || !wsStatusText) return;

        wsStatus.classList.remove('is-connected', 'is-connecting', 'is-reconnecting', 'is-disconnected', 'is-idle');
        wsStatus.classList.add(`is-${status}`);

        const labels = {
            connected: 'Connected',
            connecting: 'Connecting',
            reconnecting: 'Reconnecting',
            disconnected: 'Disconnected',
            idle: 'Idle'
        };
        wsStatusText.textContent = labels[status] || status;
    }

    function connectSocket(appId) {
        disconnectSocket();

        state.activeAppId = appId;
        state.shouldReconnect = true;
        state.reconnectAttempts = 0;
        openSocket();
    }

    function openSocket() {
        if (!state.shouldReconnect || !state.activeAppId || !state.token) return;

        setWsStatus(state.reconnectAttempts > 0 ? 'reconnecting' : 'connecting');

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const url = `${protocol}//${window.location.host}/ws?app_id=${encodeURIComponent(state.activeAppId)}&token=${encodeURIComponent(state.token)}`;

        let socket;
        try {
            socket = new WebSocket(url);
        } catch (error) {
            scheduleReconnect();
            return;
        }

        const generation = ++state.socketGeneration;
        state.socket = socket;

        socket.addEventListener('open', () => {
            if (generation !== state.socketGeneration) return;
            state.reconnectAttempts = 0;
            setWsStatus('connected');
        });

        socket.addEventListener('message', (event) => {
            if (generation !== state.socketGeneration) return;

            let payload;
            try {
                payload = JSON.parse(event.data);
            } catch (error) {
                return;
            }
            if (payload && typeof payload === 'object') handleIncomingEvent(payload);
        });

        socket.addEventListener('close', () => {
            if (generation !== state.socketGeneration) return;
            state.socket = null;
            setWsStatus('disconnected');
            scheduleReconnect();
        });

        socket.addEventListener('error', () => {
            /* A close event follows and handles the retry. */
        });
    }

    function disconnectSocket() {
        state.shouldReconnect = false;
        state.reconnectAttempts = 0;
        state.socketGeneration += 1;

        if (state.reconnectTimer) {
            window.clearTimeout(state.reconnectTimer);
            state.reconnectTimer = null;
        }

        if (state.socket) {
            try {
                state.socket.close();
            } catch (error) {
                /* ignore */
            }
            state.socket = null;
        }
    }

    function scheduleReconnect() {
        if (!state.shouldReconnect || !state.activeAppId) return;

        state.reconnectAttempts += 1;
        const delay = Math.min(1000 * Math.pow(2, state.reconnectAttempts - 1), MAX_RECONNECT_DELAY);

        setWsStatus('reconnecting');
        if (state.reconnectTimer) window.clearTimeout(state.reconnectTimer);
        state.reconnectTimer = window.setTimeout(() => {
            state.reconnectTimer = null;
            openSocket();
        }, delay);
    }

    /* ============================================================
       Event stream
       ============================================================ */

    const BADGE_CLASSES = {
        connection: 'badge-success',
        disconnection: 'badge-danger',
        subscription: 'badge-info',
        api_message: 'badge-accent',
        client_event: 'badge-warning',
        webhook_success: 'badge-success',
        webhook_error: 'badge-danger',
        subscription_error: 'badge-danger',
        api_error: 'badge-danger',
        connection_error: 'badge-danger',
        auth_error: 'badge-danger'
    };

    function handleIncomingEvent(event) {
        const normalized = {
            type: typeof event.type === 'string' && event.type ? event.type : 'event',
            event: event.event || '',
            channel: event.channel || '',
            socket_id: event.socket_id || '',
            data: event.data == null ? '' : String(event.data),
            time: event.time || new Date().toISOString()
        };

        if (state.paused) {
            state.pausedBuffer.unshift(normalized);
            if (state.pausedBuffer.length > MAX_EVENTS) {
                state.pausedBuffer.length = MAX_EVENTS;
            }
            updatePauseUI();
            return;
        }

        state.events.unshift(normalized);
        if (state.events.length > MAX_EVENTS) {
            state.events.length = MAX_EVENTS;
        }
        scheduleEventRender();
    }

    function scheduleEventRender() {
        if (state.renderFrame != null) return;
        state.renderFrame = window.requestAnimationFrame(() => {
            state.renderFrame = null;
            renderEvents();
        });
    }

    function resetEventStream() {
        state.events = [];
        state.pausedBuffer = [];
        state.paused = false;
        updatePauseUI();
        renderEvents();
    }

    function updatePauseUI() {
        pauseText.textContent = state.paused ? 'Resume' : 'Pause';
        btnPause.setAttribute('aria-pressed', String(state.paused));

        pauseIcon.innerHTML = state.paused
            ? '<polygon points="6 3 20 12 6 21 6 3"></polygon>'
            : '<rect x="6" y="4" width="4" height="16" rx="1"></rect><rect x="14" y="4" width="4" height="16" rx="1"></rect>';

        if (state.pausedBuffer.length > 0) {
            pauseCount.hidden = false;
            pauseCount.textContent = String(state.pausedBuffer.length);
        } else {
            pauseCount.hidden = true;
            pauseCount.textContent = '0';
        }
    }

    function formatEventTime(value) {
        const date = new Date(value);
        if (Number.isNaN(date.getTime())) {
            return { clock: '--:--:--', ms: '000', title: '' };
        }

        const hours = String(date.getHours()).padStart(2, '0');
        const minutes = String(date.getMinutes()).padStart(2, '0');
        const seconds = String(date.getSeconds()).padStart(2, '0');
        const milliseconds = String(date.getMilliseconds()).padStart(3, '0');

        return {
            clock: `${hours}:${minutes}:${seconds}`,
            ms: milliseconds,
            title: date.toLocaleString()
        };
    }

    function formatPayload(value) {
        if (value == null || value === '') return '';

        let parsed = value;
        if (typeof value === 'string') {
            try {
                parsed = JSON.parse(value);
            } catch (error) {
                return value;
            }
        }

        if (typeof parsed === 'string') return parsed;

        try {
            return JSON.stringify(parsed, null, 2);
        } catch (error) {
            return String(value);
        }
    }

    function emptyStateMarkup(filtered) {
        if (filtered) {
            return `
                <div class="empty-state">
                    <div class="empty-icon">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                            <circle cx="11" cy="11" r="7"></circle><path d="m20 20-3.5-3.5"></path>
                        </svg>
                    </div>
                    <p class="eyebrow">No matches</p>
                    <h2>No events found</h2>
                    <p>Try a different channel, event name or type.</p>
                    <button class="btn btn-secondary btn-sm" type="button" data-action="clear-search">Clear filter</button>
                </div>
            `;
        }

        return `
            <div class="empty-state">
                <div class="empty-icon">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                        <circle cx="12" cy="12" r="2"></circle>
                        <path d="M8.6 8.6a4.8 4.8 0 0 0 0 6.8"></path>
                        <path d="M15.4 8.6a4.8 4.8 0 0 1 0 6.8"></path>
                        <path d="M5.2 5.2a9.6 9.6 0 0 0 0 13.6"></path>
                        <path d="M18.8 5.2a9.6 9.6 0 0 1 0 13.6"></path>
                    </svg>
                </div>
                <p class="eyebrow">${state.paused ? 'Paused' : 'Listening'}</p>
                <h2>${state.paused ? 'Stream paused' : 'Waiting for events'}</h2>
                <p>${state.paused
                    ? 'Incoming events are buffered until you resume the stream.'
                    : 'Connections, subscriptions and broadcasts will appear here in real time.'}</p>
                <button class="btn btn-secondary btn-sm" type="button" data-action="send-test">Send a test event</button>
            </div>
        `;
    }

    function renderEvents() {
        const query = searchInput.value.trim().toLowerCase();
        const filtered = query
            ? state.events.filter((event) => (
                [event.type, event.event, event.channel, event.socket_id, event.data]
                    .join(' ')
                    .toLowerCase()
                    .includes(query)
            ))
            : state.events;

        const total = state.events.length;
        if (total === 0) {
            eventCount.textContent = '0 events';
        } else if (query) {
            eventCount.textContent = `${filtered.length} of ${total}`;
        } else {
            eventCount.textContent = `${total} event${total === 1 ? '' : 's'}`;
        }

        if (filtered.length === 0) {
            eventLog.innerHTML = emptyStateMarkup(total > 0 && Boolean(query));
            return;
        }

        const stream = eventLog.closest('.event-stream');
        const wasNearTop = stream ? stream.scrollTop < 40 : true;

        const rows = filtered.map((event) => {
            const time = formatEventTime(event.time);
            const badgeClass = BADGE_CLASSES[event.type] || 'badge-neutral';
            const title = event.event || event.type.replace(/_/g, ' ');
            const payload = formatPayload(event.data);

            let meta = '';
            if (event.channel) {
                meta += `<span class="event-chip"><span class="chip-key">channel</span>${escapeHtml(event.channel)}</span>`;
            }
            if (event.socket_id) {
                meta += `<span class="event-chip"><span class="chip-key">socket</span>${escapeHtml(event.socket_id)}</span>`;
            }

            const payloadMarkup = payload
                ? `<details class="event-payload">
                        <summary>Payload</summary>
                        <pre>${escapeHtml(payload)}</pre>
                   </details>`
                : '';

            return `
                <div class="event-row">
                    <div class="event-time" title="${escapeHtml(time.title)}">${time.clock}<span class="ms">.${time.ms}</span></div>
                    <div class="event-main">
                        <div class="event-line">
                            <span class="badge ${badgeClass}">${escapeHtml(event.type)}</span>
                            <span class="event-name">${escapeHtml(title)}</span>
                            ${meta}
                        </div>
                        ${payloadMarkup}
                    </div>
                </div>
            `;
        });

        eventLog.innerHTML = rows.join('');

        if (stream && wasNearTop) stream.scrollTop = 0;
    }

    btnPause.addEventListener('click', () => {
        if (state.paused) {
            state.paused = false;
            const buffered = state.pausedBuffer.length;

            if (buffered > 0) {
                state.events = state.pausedBuffer.concat(state.events).slice(0, MAX_EVENTS);
                state.pausedBuffer = [];
                renderEvents();
                toast(`Resumed · ${buffered} buffered event${buffered === 1 ? '' : 's'}`, 'info');
            }

            updatePauseUI();
        } else {
            state.paused = true;
            updatePauseUI();
        }
    });

    btnClear.addEventListener('click', () => {
        state.events = [];
        state.pausedBuffer = [];
        updatePauseUI();
        renderEvents();
    });

    function syncSearchClear() {
        clearSearchButton.hidden = searchInput.value.length === 0;
    }

    searchInput.addEventListener('input', () => {
        syncSearchClear();
        scheduleEventRender();
    });

    clearSearchButton.addEventListener('click', () => {
        searchInput.value = '';
        syncSearchClear();
        renderEvents();
        searchInput.focus();
    });

    eventLog.addEventListener('click', (event) => {
        const actionElement = event.target.closest('[data-action]');
        if (!actionElement) return;

        if (actionElement.dataset.action === 'send-test') {
            openCreatorModal();
        }
        if (actionElement.dataset.action === 'clear-search') {
            searchInput.value = '';
            syncSearchClear();
            renderEvents();
            searchInput.focus();
        }
    });

    /* ============================================================
       Static event bindings
       ============================================================ */

    loginForm.addEventListener('submit', async (event) => {
        event.preventDefault();
        clearLoginError();

        const candidate = adminTokenInput.value.trim();
        if (!candidate) {
            showLoginError('Enter your admin token to continue.');
            adminTokenInput.focus();
            return;
        }

        await authenticate(candidate);
    });

    toggleTokenButton.addEventListener('click', () => {
        const showing = adminTokenInput.type === 'text';
        adminTokenInput.type = showing ? 'password' : 'text';
        toggleTokenButton.setAttribute('aria-pressed', String(!showing));
        toggleTokenButton.setAttribute('aria-label', showing ? 'Show token' : 'Hide token');
        adminTokenInput.focus();
    });

    logoutButton.addEventListener('click', () => {
        clearStoredSession();
        state.apps = [];
        state.activeAppId = null;
        state.appToDelete = null;
        resetEventStream();
        disconnectSocket();
        setWsStatus('idle');
        stopHealthPolling();
        showLogin();
        toast('Signed out', 'info');
    });

    /* ============================================================
       Boot
       ============================================================ */

    initTheme();
    syncSearchClear();
    setWsStatus('idle');

    const savedToken = storageGet(TOKEN_KEY);
    if (savedToken) {
        state.token = savedToken;
        authenticate(savedToken, { silent: true });
    } else {
        showLogin();
    }
})();
